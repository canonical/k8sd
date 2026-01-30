package app

import (
	"context"
	"fmt"

	"github.com/canonical/k8sd/pkg/snap"
)

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
