package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) postSetClusterAPIAuthToken(s mctypes.State, r *http.Request) mctypes.Response {
	request := apiv2.ClusterAPISetAuthTokenRequest{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	if err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		return database.SetClusterAPIToken(ctx, tx, request.Token)
	}); err != nil {
		return mctypes.InternalError(err)
	}

	return mctypes.SyncResponse(true, &apiv2.SetClusterConfigResponse{})
}
