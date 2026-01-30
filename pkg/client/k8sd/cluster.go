package k8sd

import (
	"context"
	"fmt"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

func (c *k8sd) BootstrapCluster(ctx context.Context, request apiv2.BootstrapClusterRequest) (apiv2.BootstrapClusterResponse, error) {
	if err := c.app.Ready(ctx); err != nil {
		return apiv2.BootstrapClusterResponse{}, fmt.Errorf("k8sd is not ready: %w", err)
	}

	// NOTE(neoaggelos): microcluster adds an arbitrary 30 second timeout in case no context deadline is set.
	// Configure a client deadline for timeout + 30 seconds (the timeout will come from the server)
	ctx, cancel := context.WithTimeout(ctx, request.Timeout+30*time.Second)
	defer cancel()

	return query(ctx, c, "POST", apiv2.BootstrapClusterRPC, request, &apiv2.BootstrapClusterResponse{})
}

func (c *k8sd) JoinCluster(ctx context.Context, request apiv2.JoinClusterRequest) error {
	if err := c.app.Ready(ctx); err != nil {
		return fmt.Errorf("k8sd is not ready: %w", err)
	}

	// NOTE(neoaggelos): microcluster adds an arbitrary 30 second timeout in case no context deadline is set.
	// Configure a client deadline for timeout + 30 seconds (the timeout will come from the server)
	ctx, cancel := context.WithTimeout(ctx, request.Timeout+30*time.Second)
	defer cancel()

	_, err := query(ctx, c, "POST", apiv2.JoinClusterRPC, request, &apiv2.JoinClusterResponse{})
	return err
}

func (c *k8sd) RemoveNode(ctx context.Context, request apiv2.RemoveNodeRequest) error {
	// NOTE(neoaggelos): microcluster adds an arbitrary 30 second timeout in case no context deadline is set.
	// Configure a client deadline for timeout + 30 seconds (the timeout will come from the server)
	ctx, cancel := context.WithTimeout(ctx, request.Timeout+30*time.Second)
	defer cancel()

	_, err := query(ctx, c, "POST", apiv2.RemoveNodeRPC, request, &apiv2.RemoveNodeResponse{})
	return err
}

func (c *k8sd) GetJoinToken(ctx context.Context, request apiv2.GetJoinTokenRequest) (apiv2.GetJoinTokenResponse, error) {
	return query(ctx, c, "POST", apiv2.GetJoinTokenRPC, request, &apiv2.GetJoinTokenResponse{})
}
