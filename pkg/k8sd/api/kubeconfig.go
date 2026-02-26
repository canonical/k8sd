package api

import (
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/k8sd/setup"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) getKubeconfig(s mctypes.State, r *http.Request) mctypes.Response {
	req := apiv2.KubeConfigRequest{}
	if err := utils.NewStrictJSONDecoder(r.Body).Decode(&req); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	// Fetch pieces needed to render an admin kubeconfig: ca, server, token
	config, err := databaseutil.GetClusterConfig(r.Context(), s)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to retrieve cluster config: %w", err))
	}
	server := req.Server
	if req.Server == "" {
		server = fmt.Sprintf("%s:%d", s.Address().Hostname(), config.APIServer.GetSecurePort())
	}

	kubeconfig, err := setup.KubeconfigString(server, config.Certificates.GetCACert(), config.Certificates.GetAdminClientCert(), config.Certificates.GetAdminClientKey())
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get kubeconfig: %w", err))
	}

	return mctypes.SyncResponse(true, &apiv2.KubeConfigResponse{
		KubeConfig: kubeconfig,
	})
}
