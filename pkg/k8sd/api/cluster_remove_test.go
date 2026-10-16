// package api_test is used (and not api) to avoid an import cycle: testenv imports
// pkg/k8sd/app, which imports pkg/k8sd/api, so internal test files that also
// import testenv would create a cycle. export_test.go bridges the gap by
// re-exporting the unexported symbols needed here.

package api_test

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	etcdclient "github.com/canonical/k8sd/pkg/client/etcd"
	etcdmock "github.com/canonical/k8sd/pkg/client/etcd/mock"
	k8sdclient "github.com/canonical/k8sd/pkg/client/k8sd"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	"github.com/canonical/k8sd/pkg/k8sd/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
	testenv "github.com/canonical/k8sd/pkg/utils/microcluster"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
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

// TestRemoveNodeFromEtcdUnnamedMember tests that removing a node whose etcd
// member has no name (a learner whose etcd never started, e.g. left by a
// failed join) falls back to the node's etcd peer URL, and that this fallback
// only ever removes a learner.
func TestRemoveNodeFromEtcdUnnamedMember(t *testing.T) {
	cfg := types.ClusterConfig{Datastore: types.Datastore{
		Type:         utils.Pointer("etcd"),
		EtcdPort:     utils.Pointer(2379),
		EtcdPeerPort: utils.Pointer(2380),
	}}
	clusterMember := func(name, addr string) mctypes.ClusterMember {
		return mctypes.ClusterMember{ClusterMemberLocal: mctypes.ClusterMemberLocal{
			Name:    name,
			Address: mctypes.AddrPort{AddrPort: netip.MustParseAddrPort(addr)},
		}}
	}
	members := []mctypes.ClusterMember{clusterMember("cp1", "10.0.0.1:6400"), clusterMember("cp2", "10.0.0.2:6400")}
	cp1 := &etcdserverpb.Member{ID: 1, Name: "cp1", PeerURLs: []string{"https://10.0.0.1:2380"}}

	testenv.WithState(t, func(ctx context.Context, s mctypes.State) {
		removeCP2 := func(cluster *etcdmock.Cluster) error {
			mockSnap := &snapmock.Snap{Mock: snapmock.Mock{
				K8sdClient: &k8sdmock.Mock{GetClusterMembersResponse: members},
				EtcdClient: etcdmock.NewClient(cluster),
			}}
			return api.RemoveNodeFromEtcd(ctx, mockSnap, s, cfg, "cp2")
		}

		t.Run("RemovesUnnamedLearner", func(t *testing.T) {
			g := NewWithT(t)
			orphan := &etcdserverpb.Member{ID: 7, PeerURLs: []string{"https://10.0.0.2:2380"}, IsLearner: true}
			cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{cp1, orphan}}

			g.Expect(removeCP2(cluster)).To(Succeed())
			g.Expect(cluster.RemovedIDs).To(ConsistOf(orphan.ID))
			g.Expect(cluster.Members).To(ConsistOf(cp1))
		})

		t.Run("NeverRemovesUnnamedVoter", func(t *testing.T) {
			g := NewWithT(t)
			unnamedVoter := &etcdserverpb.Member{ID: 7, PeerURLs: []string{"https://10.0.0.2:2380"}}
			cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{cp1, unnamedVoter}}

			g.Expect(removeCP2(cluster)).To(MatchError(etcdclient.ErrNotLearner))
			g.Expect(cluster.RemovedIDs).To(BeEmpty())
		})

		t.Run("NothingToRemove", func(t *testing.T) {
			g := NewWithT(t)
			cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{cp1}}

			g.Expect(removeCP2(cluster)).To(Succeed())
			g.Expect(cluster.RemovedIDs).To(BeEmpty())
		})
	})
}
