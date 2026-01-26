package k8sd

import (
	"context"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

func (c *k8sd) SetClusterConfig(ctx context.Context, request apiv2.SetClusterConfigRequest) error {
	_, err := query(ctx, c, "PUT", apiv2.SetClusterConfigRPC, request, &apiv2.SetClusterConfigResponse{})
	return err
}

func (c *k8sd) GetClusterConfig(ctx context.Context) (apiv2.GetClusterConfigResponse, error) {
	return query(ctx, c, "GET", apiv2.GetClusterConfigRPC, nil, &apiv2.GetClusterConfigResponse{})
}
