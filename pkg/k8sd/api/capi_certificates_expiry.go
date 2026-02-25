package api

import (
	"fmt"
	"net/http"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	pkiutil "github.com/canonical/k8sd/pkg/utils/pki"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) postCertificatesExpiry(s types.State, r *http.Request) types.Response {
	config, err := databaseutil.GetClusterConfig(r.Context(), s)
	if err != nil {
		return types.InternalError(fmt.Errorf("failed to get cluster config: %w", err))
	}

	certificates := []string{
		config.Certificates.GetCACert(),
		config.Certificates.GetClientCACert(),
		config.Certificates.GetAdminClientCert(),
		config.Certificates.GetAPIServerKubeletClientCert(),
		config.Certificates.GetFrontProxyCACert(),
	}

	var earliestExpiry time.Time
	// Find the earliest expiry certificate
	// They should all be about the same but better double-check this.
	for _, cert := range certificates {
		if cert == "" {
			continue
		}

		cert, _, err := pkiutil.LoadCertificate(cert, "")
		if err != nil {
			return types.InternalError(fmt.Errorf("failed to load certificate: %w", err))
		}

		if earliestExpiry.IsZero() || cert.NotAfter.Before(earliestExpiry) {
			earliestExpiry = cert.NotAfter
		}
	}

	return types.SyncResponse(true, &apiv2.CertificatesExpiryResponse{
		ExpiryDate: earliestExpiry.Format(time.RFC3339),
	})
}
