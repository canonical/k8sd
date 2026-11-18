package cilium

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/canonical/k8sd/pkg/snap"
)

const ciliumPidFile = "/var/run/cilium/cilium.pid"

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
	if snap == nil || snap.CNIBinDir() == "" {
		return nil
	}
	ciliumDbg := filepath.Join(snap.CNIBinDir(), "cilium-dbg")
	if _, err := os.Stat(ciliumDbg); err != nil {
		return nil
	}

	// cilium-dbg refuses to run while the agent's pid file exists. The agent was killed during
	// container cleanup, so it never removed the file itself.
	if err := os.Remove(ciliumPidFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove %s: %w", ciliumPidFile, err)
	}

	if out, err := exec.CommandContext(ctx, ciliumDbg, "post-uninstall-cleanup", "--all-state", "--force").CombinedOutput(); err != nil {
		return fmt.Errorf("cilium-dbg cleanup failed: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
