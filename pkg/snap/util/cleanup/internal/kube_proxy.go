package internal

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
)

var (
	klogInfoRegex = regexp.MustCompile(`^I[0-9]{4}\s`)
	klogWarnRegex = regexp.MustCompile(`^W[0-9]{4}\s`)
	execCommand   = exec.CommandContext
)

func isIgnorableIPVSContainerCleanupError(stderr string) bool {
	lines := strings.Split(stderr, "\n")
	foundIPVSError := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Klog info or warning lines are non-error lines.
		if klogInfoRegex.MatchString(trimmed) || klogWarnRegex.MatchString(trimmed) {
			continue
		}

		// Must contain at least one klog error line (strings.HasPrefix(line, "E"))
		// reporting "Error flushing ipvs rules" together with "operation not permitted".
		if strings.HasPrefix(trimmed, "E") &&
			strings.Contains(trimmed, "Error flushing ipvs rules") &&
			strings.Contains(strings.ToLower(trimmed), "operation not permitted") {
			foundIPVSError = true
			continue
		}

		// Every other klog error line or fatal/error line in stderr (and any non-klog
		// non-empty error line) must ONLY be kube-proxy's generic wrapper summary lines.
		if strings.Contains(trimmed, "encountered an error while tearing down rules") &&
			(strings.Contains(trimmed, "Error running ProxyServer") || strings.Contains(trimmed, "command failed")) {
			continue
		}

		return false
	}

	return foundIPVSError
}

func TryRemoveKubeProxyRules(ctx context.Context, s snap.Snap) error {
	cmd := execCommand(ctx, filepath.Join(s.K8sBinDir(), "kube-proxy"), "--cleanup")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		if isIgnorableIPVSContainerCleanupError(stderrStr) {
			log.FromContext(ctx).Info("Ignoring IPVS flush EPERM during kube-proxy cleanup in container environment")
			return nil
		}
		return fmt.Errorf("kube-proxy --cleanup failed: %w (stderr: %s)", err, strings.TrimSpace(stderrStr))
	}
	return nil
}

func RemoveKubeProxyRules(ctx context.Context, s snap.Snap) {
	if err := TryRemoveKubeProxyRules(ctx, s); err != nil {
		log.FromContext(ctx).Error(err, "failed to run kube-proxy cleanup")
	}
}
