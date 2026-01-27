package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/microcluster/v2/state"
)

func (e *Endpoints) postSetClusterAPIAuthToken(s state.State, r *http.Request) response.Response {
	request := apiv2.ClusterAPISetAuthTokenRequest{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return response.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	if err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		return database.SetClusterAPIToken(ctx, tx, request.Token)
	}); err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, &apiv2.SetClusterConfigResponse{})
}
