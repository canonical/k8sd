package etcd_test

import (
	"context"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/etcd"
	"github.com/canonical/k8sd/pkg/client/etcd/mock"
	. "github.com/onsi/gomega"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
)

const (
	voterPeerURL  = "https://10.0.0.1:2380"
	joinerPeerURL = "https://10.0.0.2:2380"
)

func voter() *pb.Member {
	return &pb.Member{ID: 1, Name: "cp1", PeerURLs: []string{voterPeerURL}}
}

func TestAddLearner(t *testing.T) {
	t.Run("AddsLearner", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &mock.Cluster{Members: []*pb.Member{voter()}}

		result, err := mock.NewClient(cluster).AddLearner(context.Background(), joinerPeerURL)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.Adopted).To(BeFalse())
		g.Expect(result.Member).To(Equal(cluster.Member(joinerPeerURL)))
		g.Expect(result.Members).To(HaveLen(2))
	})

	// An earlier attempt reported an error but added the learner: the retry
	// is rejected and must continue with that learner instead of failing.
	for name, addErr := range map[string]error{
		"PeerURLExist": nil, // the mock rejects a duplicate peer URL itself
		"MemberExist":  rpctypes.ErrMemberExist,
	} {
		t.Run("AdoptsUnstartedLearner/"+name, func(t *testing.T) {
			g := NewWithT(t)
			orphan := &pb.Member{ID: 7, PeerURLs: []string{joinerPeerURL}, IsLearner: true}
			cluster := &mock.Cluster{Members: []*pb.Member{voter(), orphan}, AddErr: addErr}

			result, err := mock.NewClient(cluster).AddLearner(context.Background(), joinerPeerURL)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result.Adopted).To(BeTrue())
			g.Expect(result.Member.ID).To(Equal(orphan.ID))
			g.Expect(result.Members).To(HaveLen(2))
			g.Expect(cluster.RemovedIDs).To(BeEmpty())
		})
	}

	for name, existing := range map[string]*pb.Member{
		"Voter":          {ID: 7, Name: "cp2", PeerURLs: []string{joinerPeerURL}},
		"UnnamedVoter":   {ID: 7, PeerURLs: []string{joinerPeerURL}},
		"StartedLearner": {ID: 7, Name: "cp2", PeerURLs: []string{joinerPeerURL}, IsLearner: true},
	} {
		t.Run("RefusesExisting"+name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &mock.Cluster{Members: []*pb.Member{voter(), existing}}

			_, err := mock.NewClient(cluster).AddLearner(context.Background(), joinerPeerURL)
			g.Expect(err).To(MatchError(rpctypes.ErrPeerURLExist))
			g.Expect(cluster.Members).To(ContainElement(existing))
			g.Expect(cluster.RemovedIDs).To(BeEmpty())
		})
	}

	t.Run("ReturnsOtherErrors", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &mock.Cluster{Members: []*pb.Member{voter()}, AddErr: context.DeadlineExceeded}

		_, err := mock.NewClient(cluster).AddLearner(context.Background(), joinerPeerURL)
		g.Expect(err).To(MatchError(context.DeadlineExceeded))
	})
}

func TestIsMemberAddRejected(t *testing.T) {
	g := NewWithT(t)

	// Rejections: the member was not and will not be added.
	for _, err := range []error{rpctypes.ErrUnhealthy, rpctypes.ErrTooManyLearners, rpctypes.ErrPeerURLExist, rpctypes.ErrMemberExist} {
		g.Expect(etcd.IsMemberAddRejected(err)).To(BeTrue(), "%v", err)
	}
	// Unknown outcome: the add may still commit.
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, rpctypes.ErrTimeout, rpctypes.ErrLeaderChanged} {
		g.Expect(etcd.IsMemberAddRejected(err)).To(BeFalse(), "%v", err)
	}
}

func TestSweepLearnerByPeerURL(t *testing.T) {
	t.Run("RemovesLearner", func(t *testing.T) {
		g := NewWithT(t)
		orphan := &pb.Member{ID: 7, PeerURLs: []string{joinerPeerURL}, IsLearner: true}
		cluster := &mock.Cluster{Members: []*pb.Member{voter(), orphan}}

		g.Expect(mock.NewClient(cluster).SweepLearnerByPeerURL(context.Background(), joinerPeerURL, 0)).To(Succeed())
		g.Expect(cluster.RemovedIDs).To(ConsistOf(orphan.ID))
		g.Expect(cluster.Members).To(HaveLen(1))
	})

	t.Run("RemovesLearnerCommittedLate", func(t *testing.T) {
		g := NewWithT(t)
		orphan := &pb.Member{ID: 7, PeerURLs: []string{joinerPeerURL}, IsLearner: true}
		// The add only becomes visible on the second listing.
		cluster := &mock.Cluster{Members: []*pb.Member{voter()}, Late: []*pb.Member{orphan}, LateAfterLists: 1}

		g.Expect(mock.NewClient(cluster).SweepLearnerByPeerURL(context.Background(), joinerPeerURL, time.Second)).To(Succeed())
		g.Expect(cluster.RemovedIDs).To(ConsistOf(orphan.ID))
		g.Expect(cluster.Member(joinerPeerURL)).To(BeNil())
	})

	t.Run("NeverRemovesVoter", func(t *testing.T) {
		g := NewWithT(t)
		joiner := &pb.Member{ID: 7, Name: "cp2", PeerURLs: []string{joinerPeerURL}}
		cluster := &mock.Cluster{Members: []*pb.Member{voter(), joiner}}

		err := mock.NewClient(cluster).SweepLearnerByPeerURL(context.Background(), joinerPeerURL, time.Second)
		g.Expect(err).To(MatchError(etcd.ErrNotLearner))
		g.Expect(cluster.RemovedIDs).To(BeEmpty())
		g.Expect(cluster.Members).To(ContainElement(joiner))
	})

	t.Run("RetriesFailedRemoval", func(t *testing.T) {
		g := NewWithT(t)
		orphan := &pb.Member{ID: 7, PeerURLs: []string{joinerPeerURL}, IsLearner: true}
		// e.g. etcd dropped the removal because another membership change was pending.
		cluster := &mock.Cluster{Members: []*pb.Member{voter(), orphan}, RemoveErrs: []error{context.DeadlineExceeded}}

		g.Expect(mock.NewClient(cluster).SweepLearnerByPeerURL(context.Background(), joinerPeerURL, 0)).To(Succeed())
		g.Expect(cluster.RemovedIDs).To(ConsistOf(orphan.ID))
	})

	t.Run("NothingToRemove", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &mock.Cluster{Members: []*pb.Member{voter()}}

		g.Expect(mock.NewClient(cluster).SweepLearnerByPeerURL(context.Background(), joinerPeerURL, 0)).To(Succeed())
		g.Expect(cluster.RemovedIDs).To(BeEmpty())
	})
}
