package k8sd

import (
	"context"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

func (c *k8sd) KubeConfig(ctx context.Context, request apiv2.KubeConfigRequest) (apiv2.KubeConfigResponse, error) {
	return query(ctx, c, "GET", apiv2.KubeConfigRPC, request, &apiv2.KubeConfigResponse{})
}
