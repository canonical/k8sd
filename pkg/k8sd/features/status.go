package features

import (
	"context"
	"fmt"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
)

// StatusInterface defines the interface for checking the status of the built-in features.
type StatusInterface interface {
	// CheckDNS checks the status of the DNS feature.
	CheckDNS(context.Context, snap.Snap) error
	// CheckNetwork checks the status of the Network feature.
	CheckNetwork(context.Context, snap.Snap) error
	// CheckClusterReady checks whether the cluster is ready to host workloads.
	CheckClusterReady(context.Context, snap.Snap, types.ClusterConfig) error
}

// statusChecks implements the StatusInterface.
type statusChecks struct {
	checkDNS     func(context.Context, snap.Snap) error
	checkNetwork func(context.Context, snap.Snap) error
}

func (s *statusChecks) CheckDNS(ctx context.Context, snap snap.Snap) error {
	return s.checkDNS(ctx, snap)
}

func (s *statusChecks) CheckNetwork(ctx context.Context, snap snap.Snap) error {
	return s.checkNetwork(ctx, snap)
}

// CheckClusterReady returns nil when the cluster is ready to host workloads, or an error
// describing why it is not.
//
// A node reports Ready as soon as kubelet finds a CNI config on disk, which happens before
// cilium-agent is actually serving, so node readiness alone is not sufficient.
// See canonical/k8s-snap#1789.
func (s *statusChecks) CheckClusterReady(ctx context.Context, snap snap.Snap, config types.ClusterConfig) error {
	if config.Network.GetEnabled() {
		if err := s.checkNetwork(ctx, snap); err != nil {
			return fmt.Errorf("network is not ready: %w", err)
		}
	}

	return nil
}
