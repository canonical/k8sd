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

	k8sdclient "github.com/canonical/k8sd/pkg/client/k8sd"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	"github.com/canonical/k8sd/pkg/k8sd/api"
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
			GetClusterMemberFn: func(ctx context.Context, name string) (mctypes.ClusterMember, error) {
				if name == s.Name() {
					// If the wait loop erroneously checks s.Name() instead of nodeName,
					// it sees PENDING and spins until context timeout.
					return mctypes.ClusterMember{Role: "PENDING"}, nil
				}
				if name == "never-joined-node" {
					return mctypes.ClusterMember{}, k8sdclient.ErrNotFound
				}
				return mctypes.ClusterMember{}, k8sdclient.ErrNotFound
			},
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
		g.Expect(ctx.Err()).To(Succeed(), "context expired: PENDING wait loop likely spun to timeout")

		// Verify target-name correction: the first lookup in the PENDING wait loop
		// must query the target node, not s.Name().
		g.Expect(mockK8sdClient.GetClusterMemberCalledWithNames).To(HaveLen(2))
		g.Expect(mockK8sdClient.GetClusterMemberCalledWithNames[0]).To(Equal("never-joined-node"))
		g.Expect(mockK8sdClient.GetClusterMemberCalledWithNames[1]).To(Equal("never-joined-node"))
	})
}
