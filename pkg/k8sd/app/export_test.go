// Tests that need a real snap mock must live in package app_test to avoid
// import cycles, so this file bridges the gap by re-exporting unexported symbols.

package app

import (
	"context"

	"github.com/canonical/k8sd/pkg/snap"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func NewTestApp(s snap.Snap) *App {
	return &App{snap: s}
}

func OnPreRemove(a *App, ctx context.Context, s mctypes.State, force bool) error {
	return a.onPreRemove(ctx, s, force)
}
