package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) ValidateNodeTokenAccessHandler(tokenHeaderName string) func(s types.State, r *http.Request) (bool, types.Response) {
	return func(s types.State, r *http.Request) (bool, types.Response) {
		token := r.Header.Get(tokenHeaderName)
		if token == "" {
			return false, types.Unauthorized(fmt.Errorf("missing header %q", tokenHeaderName))
		}

		snap := e.provider.Snap()

		nodeToken, err := os.ReadFile(snap.NodeTokenFile())
		if err != nil {
			return false, types.InternalError(fmt.Errorf("failed to read node access token: %w", err))
		}

		if strings.TrimSpace(string(nodeToken)) != token {
			return false, types.Unauthorized(fmt.Errorf("invalid token"))
		}

		return true, nil
	}
}
