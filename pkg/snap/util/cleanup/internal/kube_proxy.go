package internal

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
)

// RemoveKubeProxyRules runs "kube-proxy --cleanup" on a best-effort basis.
// kube-proxy exits non-zero even when there was nothing to clean up (e.g. it
// reports missing KUBE-* chains with the snap's static iptables build, or
// cannot flush IPVS rules inside containers), so failures are logged, not
// returned.
func RemoveKubeProxyRules(ctx context.Context, s snap.Snap) {
	cmd := exec.CommandContext(ctx, filepath.Join(s.K8sBinDir(), "kube-proxy"), "--cleanup")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.FromContext(ctx).Error(err, "kube-proxy --cleanup reported errors; continuing", "stderr", strings.TrimSpace(stderr.String()))
	}
}
