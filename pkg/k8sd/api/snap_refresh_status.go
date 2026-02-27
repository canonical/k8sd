package api

import (
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) postSnapRefreshStatus(s mctypes.State, r *http.Request) mctypes.Response {
	req := apiv2.SnapRefreshStatusRequest{}
	if err := utils.NewStrictJSONDecoder(r.Body).Decode(&req); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	status, err := e.provider.Snap().RefreshStatus(e.Context(), req.ChangeID)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get snap refresh status: %w", err))
	}

	return mctypes.SyncResponse(true, status.ToAPI())
}
