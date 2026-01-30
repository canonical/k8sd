package k8sd

import (
	"context"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

func (c *k8sd) RefreshCertificatesPlan(ctx context.Context, request apiv2.RefreshCertificatesPlanRequest) (apiv2.RefreshCertificatesPlanResponse, error) {
	return query(ctx, c, "POST", apiv2.RefreshCertificatesPlanRPC, request, &apiv2.RefreshCertificatesPlanResponse{})
}

func (c *k8sd) RefreshCertificatesRun(ctx context.Context, request apiv2.RefreshCertificatesRunRequest) (apiv2.RefreshCertificatesRunResponse, error) {
	return query(ctx, c, "POST", apiv2.RefreshCertificatesRunRPC, request, &apiv2.RefreshCertificatesRunResponse{})
}

func (c *k8sd) RefreshCertificatesUpdate(ctx context.Context, request apiv2.RefreshCertificatesUpdateRequest) (apiv2.RefreshCertificatesUpdateResponse, error) {
	return query(ctx, c, "POST", apiv2.RefreshCertificatesUpdateRPC, request, &apiv2.RefreshCertificatesUpdateResponse{})
}

func (c *k8sd) CertificatesStatus(ctx context.Context, request apiv2.CertificatesStatusRequest) (apiv2.CertificatesStatusResponse, error) {
	return query(ctx, c, "GET", apiv2.CertificatesStatusRPC, request, &apiv2.CertificatesStatusResponse{})
}
