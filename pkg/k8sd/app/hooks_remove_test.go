// package app_test is used (and not app) to avoid an import cycle: testenv imports
// package app, so internal test files that also import testenv would create a cycle.
// export_test.go bridges the gap by re-exporting the unexported symbols needed here.

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/app"
	k8sdclient "github.com/canonical/k8sd/pkg/client/k8sd"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	testenv "github.com/canonical/k8sd/pkg/utils/microcluster"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
)

// TestOnPreRemoveNodeAbsentFromCluster tests that, when the local node is not a
// cluster member, the PENDING wait loop exits on the first iteration
// (ErrNotFound -> notPending = true) rather than spinning until context timeout.
func TestOnPreRemoveNodeAbsentFromCluster(t *testing.T) {
	testenv.WithState(t, func(ctx context.Context, s mctypes.State) {
		g := NewWithT(t)

		mockK8sdClient := &k8sdmock.Mock{
			GetClusterMemberErr: k8sdclient.ErrNotFound,
		}
		mockSnap := &snapmock.Snap{
			Mock: snapmock.Mock{K8sdClient: mockK8sdClient},
		}
		a := app.NewTestApp(mockSnap)

		// Tight deadline: without the fix the loop spins every second until context
		// expires. With the fix the loop exits on the first membership check.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := app.OnPreRemove(a, ctx, s, true)
		g.Expect(err).ToNot(HaveOccurred())

		// Context must still be valid. A spin-loop would have exhausted it.
		g.Expect(ctx.Err()).To(BeNil(), "context expired: PENDING wait loop likely timed out")
	})
}
