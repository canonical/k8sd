// package app_test is used (and not app) to avoid an import cycle: testenv imports
// package app, so internal test files that also import testenv would create a cycle.
// export_test.go bridges the gap by re-exporting the unexported symbols needed here.

package app_test

import (
	"context"
	"testing"
	"time"

	k8sdclient "github.com/canonical/k8sd/pkg/client/k8sd"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	"github.com/canonical/k8sd/pkg/k8sd/app"
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

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := app.OnPreRemove(a, ctx, s, true)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(mockK8sdClient.GetClusterMemberCalledWithNames).To(HaveLen(1))
	})
}

// TestOnPreRemoveWithoutK8sdClient verifies that rollback cleanup tolerates a
// snap that has not configured its k8sd client yet.
func TestOnPreRemoveWithoutK8sdClient(t *testing.T) {
	testenv.WithState(t, func(ctx context.Context, s mctypes.State) {
		g := NewWithT(t)
		a := app.NewTestApp(&snapmock.Snap{})

		g.Expect(app.OnPreRemove(a, ctx, s, true)).To(Succeed())
	})
}
