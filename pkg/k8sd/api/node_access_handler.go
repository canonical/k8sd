package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) ValidateNodeTokenAccessHandler(tokenHeaderName string) func(s mctypes.State, r *http.Request) (bool, mctypes.Response) {
	return func(s mctypes.State, r *http.Request) (bool, mctypes.Response) {
		token := r.Header.Get(tokenHeaderName)
		if token == "" {
			return false, mctypes.Unauthorized(fmt.Errorf("missing header %q", tokenHeaderName))
		}

		snap := e.provider.Snap()

		nodeToken, err := os.ReadFile(snap.NodeTokenFile())
		if err != nil {
			return false, mctypes.InternalError(fmt.Errorf("failed to read node access token: %w", err))
		}

		if strings.TrimSpace(string(nodeToken)) != token {
			return false, mctypes.Unauthorized(fmt.Errorf("invalid token"))
		}

		return true, nil
	}
}
