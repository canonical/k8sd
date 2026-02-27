package api

import (
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) postSnapRefresh(s mctypes.State, r *http.Request) mctypes.Response {
	req := apiv2.SnapRefreshRequest{}
	if err := utils.NewStrictJSONDecoder(r.Body).Decode(&req); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	refreshOpts, err := types.RefreshOptsFromAPI(req)
	if err != nil {
		return mctypes.BadRequest(fmt.Errorf("invalid refresh options: %w", err))
	}

	id, err := e.provider.Snap().Refresh(e.Context(), refreshOpts)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to refresh snap: %w", err))
	}

	return mctypes.SyncResponse(true, apiv2.SnapRefreshResponse{ChangeID: id})
}
