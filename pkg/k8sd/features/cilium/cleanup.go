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

	args := []string{"post-uninstall-cleanup", "--all-state", "--force"}
	if snap.Strict() {
		// --all-state removes /var/run/cilium, which opens /run and is denied under strict confinement.
		args = []string{"post-uninstall-cleanup", "--bpf-state", "--force"}
	}
	if out, err := exec.CommandContext(ctx, ciliumDbg, args...).CombinedOutput(); err != nil {
		if out := bytes.TrimSpace(out); len(out) > 0 {
			return fmt.Errorf("cilium-dbg cleanup failed: %w: %s", err, out)
		}
		return fmt.Errorf("cilium-dbg cleanup failed: %w", err)
	}
	if snap.Strict() {
		return cleanupCiliumRuntimeState(ctx)
	}
	return nil
}

// cleanupCiliumRuntimeState removes the remaining state that --all-state would: the cgroup
// mount, host links and the contents of the cilium runtime directory.
func cleanupCiliumRuntimeState(ctx context.Context) error {
	var errs []error
	if _, err := os.Stat(ciliumCgroupRoot); err == nil {
		if err := unix.Unmount(ciliumCgroupRoot, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) {
			errs = append(errs, fmt.Errorf("failed to unmount %s: %w", ciliumCgroupRoot, err))
		}
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list network interfaces: %w", err))
	}
	for _, iface := range ifaces {
		if !isCiliumHostLink(iface.Name) {
			continue
		}
		if out, err := exec.CommandContext(ctx, "ip", "link", "delete", iface.Name).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete link %s: %w: %s", iface.Name, err, bytes.TrimSpace(out)))
		}
	}

	entries, err := os.ReadDir(ciliumRunDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("failed to read %s: %w", ciliumRunDir, err))
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(ciliumRunDir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// isCiliumHostLink matches the host links removed by cilium-dbg --all-state and the health endpoint link.
func isCiliumHostLink(name string) bool {
	return strings.HasPrefix(name, "cilium_") || name == "lxc_health" || (strings.HasPrefix(name, "lxc") && len(name) == len("lxcXXXXX"))
}
