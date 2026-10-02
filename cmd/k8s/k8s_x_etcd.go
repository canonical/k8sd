package k8s

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cmdutil "github.com/canonical/k8sd/cmd/util"
	"github.com/canonical/k8sd/pkg/etcd/downgrade"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/spf13/cobra"
)

func newXEtcdCmd(env cmdutil.ExecutionEnvironment) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "x-etcd",
		Short:  "Internal etcd datastore helpers",
		Hidden: true,
	}

	cmd.AddCommand(newXEtcdPrepareDowngradeCmd(env))
	return cmd
}

func newXEtcdPrepareDowngradeCmd(env cmdutil.ExecutionEnvironment) *cobra.Command {
	var opts struct {
		targetRevision string
		timeout        time.Duration
	}

	cmd := &cobra.Command{
		Use:   "prepare-downgrade",
		Short: "Prepare the etcd cluster for a downgrade to the etcd version of a target snap revision",
		Long: `Validate and enable an etcd cluster downgrade to the etcd version shipped in the
given target snap revision, and wait until the local member's storage version has
been migrated. This must run while the current (newer) etcd binary is still
running, i.e. from the snap pre-refresh hook before the binary is swapped.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if err := etcdPrepareDowngrade(cmd, env, opts.targetRevision, opts.timeout); err != nil {
				cmd.PrintErrf("Error: %v\n", err)
				env.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&opts.targetRevision, "target-revision", "", "snap revision being refreshed to (e.g. 1234); its etcd version is read from the mounted revision's bom.json")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 5*time.Minute, "timeout for waiting for the storage version migration")
	_ = cmd.MarkFlagRequired("target-revision")

	return cmd
}

func etcdPrepareDowngrade(cmd *cobra.Command, env cmdutil.ExecutionEnvironment, targetRevision string, timeout time.Duration) error {
	ctx := cmd.Context()
	lg := log.FromContext(ctx)
	snap := env.Snap

	// Only control-plane nodes with the managed etcd datastore participate.
	etcdDataDir := filepath.Join(snap.EtcdDir(), "data")
	if _, err := os.Stat(etcdDataDir); err != nil {
		if os.IsNotExist(err) {
			lg.Info("No etcd data directory found, skipping downgrade preparation (worker node, external datastore, or not bootstrapped)")
			return nil
		}
		return fmt.Errorf("failed to stat etcd data directory: %w", err)
	}

	currentVersion, err := downgrade.VersionFromBOM(snapDirOf(snap))
	if err != nil {
		return fmt.Errorf("failed to determine current etcd version: %w", err)
	}

	targetSnapDir := filepath.Join(filepath.Dir(snapDirOf(snap)), targetRevision)
	targetVersion, err := downgrade.VersionFromBOM(targetSnapDir)
	if err != nil {
		return fmt.Errorf("failed to determine target etcd version: %w", err)
	}

	if !targetVersion.LessThan(currentVersion) {
		lg.Info("Target revision does not downgrade etcd, nothing to do",
			"current-version", currentVersion.String(), "target-version", targetVersion.String())
		return nil
	}

	if !currentVersion.IsValidDowngradeTarget(targetVersion) {
		return fmt.Errorf("cannot downgrade etcd from %s to %s: only single minor version downgrades are supported", currentVersion, targetVersion)
	}

	endpoints, err := localEtcdEndpoints(snap.ServiceArgumentsDir())
	if err != nil {
		return fmt.Errorf("failed to determine local etcd endpoints: %w", err)
	}

	client, err := snap.EtcdClient(endpoints)
	if err != nil {
		return fmt.Errorf("failed to create etcd client: %w", err)
	}
	defer client.Close()

	return downgrade.PrepareDowngrade(ctx, lg, client, targetVersion, timeout)
}

// snapDirOf returns the snap directory ($SNAP) of the current revision.
func snapDirOf(snap interface{ K8sBinDir() string }) string {
	return filepath.Dir(snap.K8sBinDir())
}

// localEtcdEndpoints derives the local etcd client endpoints from the
// --listen-client-urls argument in the etcd service arguments file.
func localEtcdEndpoints(serviceArgumentsDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(serviceArgumentsDir, "etcd"))
	if err != nil {
		return nil, fmt.Errorf("failed to read etcd service arguments: %w", err)
	}

	for _, field := range strings.Fields(string(data)) {
		if urls, ok := strings.CutPrefix(field, "--listen-client-urls="); ok {
			var endpoints []string
			for _, u := range strings.Split(urls, ",") {
				if u = strings.TrimSpace(u); u != "" {
					endpoints = append(endpoints, u)
				}
			}
			if len(endpoints) > 0 {
				return endpoints, nil
			}
		}
	}

	// Fall back to the default etcd client port on localhost.
	localhost, err := utils.GetLocalhostAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get localhost address: %w", err)
	}
	return []string{fmt.Sprintf("https://%s", utils.JoinHostPort(localhost.String(), 2379))}, nil
}
