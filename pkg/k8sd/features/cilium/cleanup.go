package cilium

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/canonical/k8sd/pkg/snap"
	"golang.org/x/sys/unix"
)

const (
	ciliumPidFile    = "/var/run/cilium/cilium.pid"
	ciliumRunDir     = "/run/cilium"
	ciliumCgroupRoot = "/run/cilium/cgroupv2"
	ciliumLinkPrefix = "cilium_"
	ciliumHostPrefix = "lxc"
	ciliumHostLen    = len("lxcXXXXX")
)

func CleanupNetwork(ctx context.Context, snap snap.Snap) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var errs []error

	if err := cleanupCiliumState(ctx, snap); err != nil {
		errs = append(errs, err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(append(errs, ctxErr)...)
		}
	}

	for _, cmd := range []string{"iptables", "ip6tables", "iptables-legacy", "ip6tables-legacy"} {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}

		out, err := exec.CommandContext(ctx, fmt.Sprintf("%s-save", cmd)).Output()
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to read %s rules: %w", cmd, err))
			if ctxErr := ctx.Err(); ctxErr != nil {
				errs = append(errs, ctxErr)
				break
			}
			continue
		}

		var modified bool
		lines := strings.Split(string(out), "\n")
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), "cilium") {
				lines[i] = ""
				modified = true
			}
		}

		if !modified {
			continue
		}

		restore := exec.CommandContext(ctx, fmt.Sprintf("%s-restore", cmd))
		restore.Stdin = strings.NewReader(strings.Join(lines, "\n"))
		if err := restore.Run(); err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to restore %s rules: %w", cmd, err))
			if ctxErr := ctx.Err(); ctxErr != nil {
				errs = append(errs, ctxErr)
				break
			}
			continue
		}
	}

	return errors.Join(errs...)
}

// cleanupCiliumState runs cilium-dbg post-uninstall-cleanup, which removes Cilium's network
// interfaces, BPF state and state directories from the host.
func cleanupCiliumState(ctx context.Context, snap snap.Snap) error {
	// cilium-dbg refuses to run while the agent's pid file exists. The agent was killed during
	// container cleanup, so it never removed the file itself.
	if err := os.Remove(ciliumPidFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove %s: %w", ciliumPidFile, err)
	}

	if snap == nil || snap.CNIBinDir() == "" {
		return nil
	}
	ciliumDbg := filepath.Join(snap.CNIBinDir(), "cilium-dbg")
	if _, err := os.Stat(ciliumDbg); err != nil {
		return nil
	}

	args := []string{"post-uninstall-cleanup"}
	if snap.Strict() {
		// Without --bpf-state, cilium-dbg defaults to --all-state and calls
		// os.RemoveAll("/var/run/cilium"), which opens parent directory /run/ and
		// triggers an AppArmor denial under strict confinement.
		args = append(args, "--bpf-state")
	} else {
		args = append(args, "--all-state")
	}
	args = append(args, "--force")

	if out, err := exec.CommandContext(ctx, ciliumDbg, args...).CombinedOutput(); err != nil {
		if out := bytes.TrimSpace(out); len(out) > 0 {
			return fmt.Errorf("cilium-dbg cleanup failed: %w: %s", err, out)
		}
		return fmt.Errorf("cilium-dbg cleanup failed: %w", err)
	}
	if snap.Strict() {
		var strictErrs []error
		if err := unmountCiliumCgroup(ciliumCgroupRoot); err != nil {
			strictErrs = append(strictErrs, err)
		}
		if err := removeCiliumHostLinks(ctx); err != nil {
			strictErrs = append(strictErrs, err)
		}
		if err := cleanDirContents(ciliumRunDir); err != nil {
			strictErrs = append(strictErrs, fmt.Errorf("failed to clean %s: %w", ciliumRunDir, err))
		}
		return errors.Join(strictErrs...)
	}
	return nil
}

func unmountCiliumCgroup(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	if err := unix.Unmount(path, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("failed to unmount %s: %w", path, err)
	}
	return nil
}

func isCiliumHostLink(name string) bool {
	return strings.HasPrefix(name, ciliumLinkPrefix) ||
		name == "lxc_health" ||
		(strings.HasPrefix(name, ciliumHostPrefix) && len(name) == ciliumHostLen)
}

func removeCiliumHostLinks(ctx context.Context) error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed to list network interfaces: %w", err)
	}
	var errs []error
	for _, iface := range ifaces {
		if !isCiliumHostLink(iface.Name) {
			continue
		}
		out, err := exec.CommandContext(ctx, "ip", "link", "delete", iface.Name).CombinedOutput()
		if err != nil {
			msg := string(out)
			if strings.Contains(msg, "Cannot find device") || strings.Contains(msg, "no such device") {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to delete link %s: %w: %s", iface.Name, err, bytes.TrimSpace(out)))
		}
	}
	return errors.Join(errs...)
}

// cleanDirContents removes all entries inside dir using os.OpenRoot so removal
// stays strictly relative to dir's file descriptor and never opens filepath.Dir(dir).
func cleanDirContents(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("unexpected non-directory path: %s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	var errs []error
	for _, entry := range entries {
		if rmErr := root.RemoveAll(entry.Name()); rmErr != nil {
			errs = append(errs, rmErr)
		}
	}
	return errors.Join(errs...)
}
