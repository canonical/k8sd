// package api_test is used (and not api) to avoid an import cycle: testenv imports
// pkg/k8sd/app, which imports pkg/k8sd/api, so internal test files that also
// import testenv would create a cycle. export_test.go bridges the gap by
// re-exporting the unexported symbols needed here.

package api_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"

	etcdclient "github.com/canonical/k8sd/pkg/client/etcd"
	k8sdclient "github.com/canonical/k8sd/pkg/client/k8sd"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	"github.com/canonical/k8sd/pkg/k8sd/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
	testenv "github.com/canonical/k8sd/pkg/utils/microcluster"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
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

// fakeEtcdCluster implements clientv3.Cluster. Only MemberList and MemberRemove are overridden.
type fakeEtcdCluster struct {
	clientv3.Cluster
	members         []*pb.Member
	memberListErr   error
	removedMemberID uint64
}

func (f *fakeEtcdCluster) MemberList(_ context.Context, _ ...clientv3.OpOption) (*clientv3.MemberListResponse, error) {
	if f.memberListErr != nil {
		return nil, f.memberListErr
	}
	return (*clientv3.MemberListResponse)(&pb.MemberListResponse{Members: f.members}), nil
}

func (f *fakeEtcdCluster) MemberRemove(_ context.Context, id uint64) (*clientv3.MemberRemoveResponse, error) {
	f.removedMemberID = id
	return (*clientv3.MemberRemoveResponse)(&pb.MemberRemoveResponse{}), nil
}

// fakeEtcdMaintenance implements clientv3.Maintenance. Only Status is overridden.
type fakeEtcdMaintenance struct {
	clientv3.Maintenance
	leaderID uint64
}

func (f *fakeEtcdMaintenance) Status(_ context.Context, _ string) (*clientv3.StatusResponse, error) {
	return (*clientv3.StatusResponse)(&pb.StatusResponse{Leader: f.leaderID}), nil
}

func TestRemoveNodeFromDatastoreAndMicrocluster(t *testing.T) {
	survivor := &pb.Member{ID: 1, Name: "cp1", ClientURLs: []string{"https://10.0.0.1:2379"}}
	dead := &pb.Member{ID: 2, Name: "cp2", ClientURLs: []string{"https://10.0.0.2:2379"}}

	etcdConfig := types.ClusterConfig{Datastore: types.Datastore{Type: utils.Pointer("etcd"), EtcdPort: utils.Pointer(2379)}}

	newSnap := func(cluster *fakeEtcdCluster) (*snapmock.Snap, *k8sdmock.Mock) {
		etcd := clientv3.NewCtxClient(context.Background())
		etcd.Cluster = cluster
		// The survivor is the etcd leader, so no leadership transfer is attempted.
		etcd.Maintenance = &fakeEtcdMaintenance{leaderID: survivor.ID}

		k8sd := &k8sdmock.Mock{
			GetClusterMembersResponse: []mctypes.ClusterMember{
				{ClusterMemberLocal: mctypes.ClusterMemberLocal{Name: "cp1", Address: mctypes.AddrPort{AddrPort: netip.MustParseAddrPort("10.0.0.1:6400")}}},
				{ClusterMemberLocal: mctypes.ClusterMemberLocal{Name: "cp2", Address: mctypes.AddrPort{AddrPort: netip.MustParseAddrPort("10.0.0.2:6400")}}},
			},
			GetClusterMemberResponse: mctypes.ClusterMember{
				ClusterMemberLocal: mctypes.ClusterMemberLocal{Name: "cp2", Address: mctypes.AddrPort{AddrPort: netip.MustParseAddrPort("10.0.0.2:6400")}},
				Role:               "spare",
			},
		}
		return &snapmock.Snap{Mock: snapmock.Mock{K8sdClient: k8sd, EtcdClient: &etcdclient.Client{Client: etcd}}}, k8sd
	}

	t.Run("EtcdNoQuorumWithForce", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &fakeEtcdCluster{memberListErr: context.DeadlineExceeded}
		snap, k8sd := newSnap(cluster)

		err := api.RemoveNodeFromDatastoreAndMicrocluster(context.Background(), nil, snap, "cp2", etcdConfig, true, false)
		g.Expect(err).To(MatchError(ContainSubstring("etcd has no quorum")))
		g.Expect(err).To(MatchError(ContainSubstring("cp2 is still an etcd member")))
		g.Expect(err).To(MatchError(ContainSubstring("--force-new-cluster")))
		g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		g.Expect(k8sd.RemoveClusterMemberName).To(BeEmpty(), "node must not be removed from microcluster")
	})

	t.Run("EtcdNoQuorumWithForceConfirmedWorker", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &fakeEtcdCluster{memberListErr: context.DeadlineExceeded}
		snap, k8sd := newSnap(cluster)

		err := api.RemoveNodeFromDatastoreAndMicrocluster(context.Background(), nil, snap, "cp2", etcdConfig, true, true)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(k8sd.RemoveClusterMemberName).To(Equal("cp2"))
	})

	t.Run("NodeAbsentFromEtcdWithForce", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &fakeEtcdCluster{members: []*pb.Member{survivor}}
		snap, k8sd := newSnap(cluster)

		err := api.RemoveNodeFromDatastoreAndMicrocluster(context.Background(), nil, snap, "cp2", etcdConfig, true, false)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cluster.removedMemberID).To(BeZero())
		g.Expect(k8sd.RemoveClusterMemberName).To(Equal("cp2"))
		g.Expect(k8sd.RemoveClusterMemberForce).To(BeTrue())
	})

	t.Run("HealthyRemoval", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &fakeEtcdCluster{members: []*pb.Member{survivor, dead}}
		snap, k8sd := newSnap(cluster)

		err := api.RemoveNodeFromDatastoreAndMicrocluster(context.Background(), nil, snap, "cp2", etcdConfig, false, false)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cluster.removedMemberID).To(Equal(dead.ID))
		g.Expect(k8sd.RemoveClusterMemberName).To(Equal("cp2"))
		g.Expect(k8sd.RemoveClusterMemberForce).To(BeFalse())
	})

	t.Run("EtcdNoQuorumWithoutForce", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &fakeEtcdCluster{memberListErr: context.DeadlineExceeded}
		snap, k8sd := newSnap(cluster)

		err := api.RemoveNodeFromDatastoreAndMicrocluster(context.Background(), nil, snap, "cp2", etcdConfig, false, false)
		g.Expect(err).To(MatchError(ContainSubstring("etcd has no quorum")))
		g.Expect(k8sd.RemoveClusterMemberName).To(BeEmpty())
	})
}

func TestCanContinueAfterDatastoreRemovalError(t *testing.T) {
	for _, tc := range []struct {
		name            string
		force           bool
		datastoreType   string
		confirmedWorker bool
		expected        bool
	}{
		{name: "NoForce", force: false, datastoreType: "etcd", expected: false},
		{name: "NoForceWorker", force: false, datastoreType: "etcd", confirmedWorker: true, expected: false},
		{name: "ForceEtcdControlPlane", force: true, datastoreType: "etcd", expected: false},
		{name: "ForceEtcdConfirmedWorker", force: true, datastoreType: "etcd", confirmedWorker: true, expected: true},
		{name: "ForceExternal", force: true, datastoreType: "external", expected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(api.CanContinueAfterDatastoreRemovalError(tc.force, tc.datastoreType, tc.confirmedWorker)).To(Equal(tc.expected))
		})
	}
}
