// Tests that need a real snap mock must live in package api_test to avoid
// import cycles, so this file bridges the gap by re-exporting unexported symbols.

package api

import (
	"context"

	"github.com/canonical/k8sd/pkg/snap"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

var RemoveNodeFromMicrocluster = func(ctx context.Context, s mctypes.State, nodeName string, force bool, snap snap.Snap) error {
	return removeNodeFromMicrocluster(ctx, s, nodeName, force, snap)
}
