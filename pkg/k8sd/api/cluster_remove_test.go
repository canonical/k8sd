// package api_test is used (and not api) to avoid an import cycle: testenv imports
// pkg/k8sd/app, which imports pkg/k8sd/api, so internal test files that also
// import testenv would create a cycle. export_test.go bridges the gap by
// re-exporting the unexported symbols needed here.

package api_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/api"
	k8sdclient "github.com/canonical/k8sd/pkg/client/k8sd"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	testenv "github.com/canonical/k8sd/pkg/utils/microcluster"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
)

// TestRemoveNodeFromMicroclusterAbsentFromCluster tests that, when the target node
// is not a cluster member, the PENDING wait loop exits on the first iteration
// (ErrNotFound -> notPending = true) rather than spinning until context timeout,
// and that DeleteClusterMember returning not-found is treated as success.
func TestRemoveNodeFromMicroclusterAbsentFromCluster(t *testing.T) {
	testenv.WithState(t, func(ctx context.Context, s mctypes.State) {
		g := NewWithT(t)

		mockK8sdClient := &k8sdmock.Mock{
			GetClusterMemberErr:    k8sdclient.ErrNotFound,
			RemoveClusterMemberErr: os.ErrNotExist,
		}
		mockSnap := &snapmock.Snap{
			Mock: snapmock.Mock{K8sdClient: mockK8sdClient},
		}

		// Tight deadline: without the fix the loop spins every second until context
		// expires. With the fix the loop exits on the first membership check.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := api.RemoveNodeFromMicrocluster(ctx, s, "never-joined-node", false, mockSnap)
		g.Expect(err).ToNot(HaveOccurred())

		// Context must still be valid. A spin-loop would have exhausted it.
		g.Expect(ctx.Err()).To(BeNil(), "context expired: PENDING wait loop likely spun to timeout")
	})
}
