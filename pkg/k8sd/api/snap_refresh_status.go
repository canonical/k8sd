package api

import (
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/canonical/microcluster/v3/microcluster/rest/response"
	"github.com/canonical/microcluster/v3/state"
)

func (e *Endpoints) postSnapRefreshStatus(s state.State, r *http.Request) response.Response {
	req := apiv2.SnapRefreshStatusRequest{}
	if err := utils.NewStrictJSONDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	status, err := e.provider.Snap().RefreshStatus(e.Context(), req.ChangeID)
	if err != nil {
		return response.InternalError(fmt.Errorf("failed to get snap refresh status: %w", err))
	}

	return response.SyncResponse(true, status.ToAPI())
}
