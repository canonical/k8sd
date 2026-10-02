package downgrade

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils/control"
)

// RecoverConfig configures the etcd downgrade recovery.
type RecoverConfig struct {
	// SnapDir is the snap directory of the current revision ($SNAP).
	SnapDir string
	// SnapCommonDir is the snap common directory ($SNAP_COMMON).
	SnapCommonDir string
	// EtcdDataDir is the etcd data directory, e.g. $SNAP_COMMON/var/lib/etcd/data.
	EtcdDataDir string
	// EtcdPKIDir is the directory with the etcd certificates.
	EtcdPKIDir string
	// EtcdEndpoints are the client endpoints of the local etcd member,
	// e.g. ["https://127.0.0.1:2379"].
	EtcdEndpoints []string
	// NewClient creates an etcd client for the given endpoints.
	NewClient func(endpoints []string) (Client, error)
	// StopEtcd stops the etcd snap service.
	StopEtcd func(ctx context.Context) error
	// StartEtcd starts the etcd snap service.
	StartEtcd func(ctx context.Context) error
	// Timeout bounds the whole recovery operation.
	Timeout time.Duration
}

// NeedsRecovery reports whether the etcd data directory was written by a newer
// etcd minor version than the binary shipped in the current snap revision.
// In that state the bundled etcd binary refuses to start, so the downgrade
// protocol must be completed with a compatible (newer) binary first.
func NeedsRecovery(snapDir, etcdDataDir string) (bool, *Version, *Version, error) {
	storageVersion, err := StorageVersionFromDataDir(etcdDataDir)
	if err != nil {
		return false, nil, nil, fmt.Errorf("failed to detect etcd storage version: %w", err)
	}
	if storageVersion == nil {
		// No storage version recorded: either no data yet or a pre-3.6 data
		// directory. Both are handled by etcd itself.
		return false, nil, nil, nil
	}

	binaryVersion, err := VersionFromBOM(snapDir)
	if err != nil {
		return false, nil, nil, fmt.Errorf("failed to detect bundled etcd version: %w", err)
	}

	if binaryVersion.LessThan(*storageVersion) {
		return true, storageVersion, &binaryVersion, nil
	}
	return false, storageVersion, &binaryVersion, nil
}

// FindCompatibleBinary scans the installed snap revisions for an etcd binary
// whose version is at least the given storage version, so that it can open the
// existing data directory. The current revision is skipped.
//
// snapd keeps previous snap revisions mounted under /snap/<name>/<revision>,
// which makes the previously running (newer) etcd binary available for
// recovery after a downgrade.
func FindCompatibleBinary(snapDir string, storageVersion Version) (string, error) {
	// snapDir is /snap/<name>/<revision>; the parent directory contains one
	// subdirectory per installed revision.
	revisionsDir := filepath.Dir(snapDir)
	currentRevision := filepath.Base(snapDir)

	entries, err := os.ReadDir(revisionsDir)
	if err != nil {
		return "", fmt.Errorf("failed to list snap revisions in %q: %w", revisionsDir, err)
	}

	// Sort revisions numerically, newest first, to prefer the most recent
	// compatible binary.
	revisions := []int{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == currentRevision || e.Name() == "current" {
			continue
		}
		if rev, err := strconv.Atoi(e.Name()); err == nil {
			revisions = append(revisions, rev)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(revisions)))

	for _, rev := range revisions {
		revisionDir := filepath.Join(revisionsDir, strconv.Itoa(rev))
		candidate := filepath.Join(revisionDir, "bin", "etcd")
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		version, err := VersionFromBOM(revisionDir)
		if err != nil {
			continue
		}
		if !version.LessThan(storageVersion) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no etcd binary with version >= %s found in %q", storageVersion, revisionsDir)
}

// Recover completes an interrupted etcd downgrade: it starts a compatible
// (newer) etcd binary from a previous snap revision against the existing data
// directory, enables the downgrade to the bundled binary's version, waits for
// the storage migration, and finally starts the real etcd service.
//
// This is required after a snap downgrade that happened without preparing the
// etcd downgrade first (e.g. `snap revert`, or a refresh from a revision that
// did not include the pre-refresh preparation), because the bundled older etcd
// binary refuses to start against the newer storage version.
func Recover(ctx context.Context, lg log.Logger, cfg RecoverConfig) error {
	needed, storageVersion, binaryVersion, err := NeedsRecovery(cfg.SnapDir, cfg.EtcdDataDir)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}

	lg.Info("etcd data directory was written by a newer etcd version, recovering downgrade",
		"storage-version", storageVersion.String(), "binary-version", binaryVersion.String())

	if !storageVersion.IsValidDowngradeTarget(*binaryVersion) {
		return fmt.Errorf("cannot downgrade etcd storage from %s to %s: only single minor version downgrades are supported; restore from a snapshot instead", storageVersion, binaryVersion)
	}

	compatibleBinary, err := FindCompatibleBinary(cfg.SnapDir, *storageVersion)
	if err != nil {
		return fmt.Errorf("failed to find an etcd binary compatible with storage version %s: %w", storageVersion, err)
	}
	lg.Info("Found compatible etcd binary for recovery", "path", compatibleBinary)

	// Stop the (crash-looping) etcd service to free the ports and the data dir lock.
	if err := cfg.StopEtcd(ctx); err != nil {
		return fmt.Errorf("failed to stop etcd service: %w", err)
	}

	if err := runWithCompatibleBinary(ctx, lg, cfg, compatibleBinary, *binaryVersion); err != nil {
		return err
	}

	lg.Info("Starting etcd service with downgraded data directory")
	if err := cfg.StartEtcd(ctx); err != nil {
		return fmt.Errorf("failed to start etcd service after downgrade recovery: %w", err)
	}
	return nil
}

// runWithCompatibleBinary starts the given etcd binary with the snap's etcd
// arguments, runs the downgrade protocol against it, and stops it again.
func runWithCompatibleBinary(ctx context.Context, lg log.Logger, cfg RecoverConfig, binary string, target Version) error {
	args, err := os.ReadFile(filepath.Join(cfg.SnapCommonDir, "args", "etcd"))
	if err != nil {
		return fmt.Errorf("failed to read etcd service arguments: %w", err)
	}

	// Run the binary from its own revision directory so that it picks up its
	// matching shared libraries.
	revisionDir := filepath.Dir(filepath.Dir(binary))
	cmd := exec.CommandContext(ctx, binary, strings.Fields(string(args))...)
	cmd.Env = append(os.Environ(), libraryPathEnv(revisionDir)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	lg.Info("Starting compatible etcd binary for downgrade recovery", "binary", binary)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start compatible etcd binary: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	client, err := cfg.NewClient(cfg.EtcdEndpoints)
	if err != nil {
		return fmt.Errorf("failed to create etcd client: %w", err)
	}
	defer client.Close()

	// Wait for the recovered member to become healthy before running the
	// downgrade protocol.
	if err := control.WaitUntilReady(ctx, func() (bool, error) {
		_, err := client.Status(ctx, cfg.EtcdEndpoints[0])
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("recovered etcd member did not become healthy: %w", err)
	}

	return PrepareDowngrade(ctx, lg, client, target, cfg.Timeout)
}

// libraryPathEnv returns LD_LIBRARY_PATH-style environment entries pointing at
// the library directories of the given snap revision, so that a binary from
// that revision loads its matching shared libraries.
func libraryPathEnv(revisionDir string) []string {
	var dirs []string
	for _, pattern := range []string{
		filepath.Join(revisionDir, "lib"),
		filepath.Join(revisionDir, "usr", "lib"),
		filepath.Join(revisionDir, "lib", "*-linux-gnu"),
		filepath.Join(revisionDir, "usr", "lib", "*-linux-gnu"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		dirs = append(dirs, matches...)
	}
	if len(dirs) == 0 {
		return nil
	}
	ld := strings.Join(dirs, ":")
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		ld = ld + ":" + existing
	}
	return []string{"LD_LIBRARY_PATH=" + ld}
}
