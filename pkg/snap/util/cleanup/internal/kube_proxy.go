package internal

import (
	"context"
	"os/exec"
	"path/filepath"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
)

func TryRemoveKubeProxyRules(ctx context.Context, s snap.Snap) error {
	cmd := exec.CommandContext(ctx, filepath.Join(s.K8sBinDir(), "kube-proxy"), "--cleanup")
	return cmd.Run()
}

func RemoveKubeProxyRules(ctx context.Context, s snap.Snap) {
	if err := TryRemoveKubeProxyRules(ctx, s); err != nil {
		log.FromContext(ctx).Error(err, "failed to run kube-proxy cleanup")
	}
}
