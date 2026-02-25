package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) postSetClusterAPIAuthToken(s types.State, r *http.Request) types.Response {
	request := apiv2.ClusterAPISetAuthTokenRequest{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return types.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	if err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		return database.SetClusterAPIToken(ctx, tx, request.Token)
	}); err != nil {
		return types.InternalError(err)
	}

	return types.SyncResponse(true, &apiv2.SetClusterConfigResponse{})
}
