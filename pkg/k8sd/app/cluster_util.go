package app

import (
	"context"
	"fmt"

	"github.com/canonical/k8sd/pkg/k8sd/setup"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	"github.com/canonical/k8sd/pkg/utils/control"
)

// startControlPlaneServices starts the control plane services based on the datastore type.
func startControlPlaneServices(ctx context.Context, snap snap.Snap, cfg types.ClusterConfig) error {
	// Start services
	datastore := cfg.Datastore.GetType()
	switch datastore {
	case "etcd":
		if err := snaputil.StartEtcdServices(ctx, snap); err != nil {
			return fmt.Errorf("failed to start etcd services: %w", err)
		}
	case "external":
		// For external datastore, we do not start any services here.
	default:
		return fmt.Errorf("unsupported datastore %s, must be one of %v", datastore, setup.SupportedDatastores)
	}

	if err := snaputil.StartControlPlaneServices(ctx, snap, cfg); err != nil {
		return fmt.Errorf("failed to start control plane services: %w", err)
	}
	return nil
}

func waitApiServerReady(ctx context.Context, snap snap.Snap) error {
	// Wait for API server to come up
	client, err := snap.KubernetesClient("")
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	if err := client.WaitKubernetesEndpointAvailable(ctx); err != nil {
		return fmt.Errorf("kubernetes endpoints not ready yet: %w", err)
	}

	return nil
}

// waitNodeRegistered waits until this node's Node object exists, not until
// it's Ready: Ready requires a CNI, which isn't installed when managed
// networking is disabled.
func waitNodeRegistered(ctx context.Context, snap snap.Snap, nodeName string) error {
	client, err := snap.KubernetesClient("")
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	if err := control.WaitUntilReady(ctx, func() (bool, error) {
		return client.NodeRegistered(ctx, nodeName)
	}); err != nil {
		return fmt.Errorf("node did not register in time: %w", err)
	}

	return nil
}
