package snaputil

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils/control"
)

// deprecatedKubeletFlags lists kubelet flags that older snap releases wrote
// to the persisted arguments file but that newer Kubernetes versions no
// longer accept, causing kubelet to fail to start.
var deprecatedKubeletFlags = []string{
	// removed in Kubernetes 1.37; nodes upgrading from 1.36 may still have
	// this flag in their arguments file.
	"--containerd",
}

// RemoveDeprecatedKubeletFlags strips deprecated flags from the kubelet
// arguments file, restarting kubelet if any were actually removed.
//
// This only touches the local filesystem and the kubelet service, with no
// dependency on the k8sd API, database, or any other part of the daemon
// being up. That makes it safe to invoke very early - e.g. directly from a
// snap refresh hook, before kubelet's own systemd unit is (re)started with
// the new binary - which closes the race where kubelet starts with a
// deprecated flag before k8sd has had a chance to clean it up.
func RemoveDeprecatedKubeletFlags(ctx context.Context, snap snap.Snap) error {
	mustRestart, err := UpdateServiceArguments(snap, "kubelet", nil, deprecatedKubeletFlags)
	if err != nil {
		return fmt.Errorf("failed to remove deprecated kubelet flags: %w", err)
	}

	if mustRestart {
		// This may fail if other controllers try to restart the services at the same time, hence the retry.
		if err := control.RetryFor(ctx, 5, 5*time.Second, func() error {
			if err := snap.RestartServices(ctx, []string{"kubelet"}); err != nil {
				return fmt.Errorf("failed to restart kubelet after removing deprecated flags: %w", err)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed after retry: %w", err)
		}
	}

	return nil
}
