package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/canonical/k8sd/pkg/k8sd/database"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func ValidateCAPIAuthTokenAccessHandler(tokenHeaderName string) func(s mctypes.State, r *http.Request) (bool, mctypes.Response) {
	return func(s mctypes.State, r *http.Request) (bool, mctypes.Response) {
		token := r.Header.Get(tokenHeaderName)
		if token == "" {
			return false, mctypes.Unauthorized(fmt.Errorf("missing header %q", tokenHeaderName))
		}

		var tokenIsValid bool
		if err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
			var err error
			tokenIsValid, err = database.ValidateClusterAPIToken(ctx, tx, token)
			if err != nil {
				return fmt.Errorf("failed to check CAPI auth token: %w", err)
			}
			return nil
		}); err != nil {
			return false, mctypes.InternalError(fmt.Errorf("check CAPI auth token database transaction failed: %w", err))
		}
		if !tokenIsValid {
			return false, mctypes.Unauthorized(fmt.Errorf("invalid token"))
		}

		return true, nil
	}
}
