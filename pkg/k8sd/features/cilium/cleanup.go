package cilium

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/canonical/k8sd/pkg/snap"
)

func CleanupNetwork(ctx context.Context, snap snap.Snap) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	_ = os.Remove("/var/run/cilium/cilium.pid")

	var errs []error

	if snap != nil {
		if cniBinDir := snap.CNIBinDir(); cniBinDir != "" {
			ciliumDbg := filepath.Join(cniBinDir, "cilium-dbg")
			if _, err := os.Stat(ciliumDbg); err == nil {
				if err := exec.CommandContext(ctx, ciliumDbg, "post-uninstall-cleanup", "--all-state", "--force").Run(); err != nil {
					errs = append(errs, fmt.Errorf("cilium-dbg cleanup failed: %w", err))
					if ctx.Err() != nil {
						return errors.Join(errs...)
					}
				}
			}
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
			if ctx.Err() != nil {
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
			errs = append(errs, fmt.Errorf("failed to restore %s rules: %w", cmd, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
	}

	return errors.Join(errs...)
}
