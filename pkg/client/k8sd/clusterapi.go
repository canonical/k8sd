package k8sd

import (
	"context"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

func (c *k8sd) SetClusterAPIAuthToken(ctx context.Context, request apiv2.ClusterAPISetAuthTokenRequest) error {
	_, err := query(ctx, c, "POST", apiv2.ClusterAPISetAuthTokenRPC, request, &apiv2.ClusterAPIGetJoinTokenResponse{})
	return err
}
