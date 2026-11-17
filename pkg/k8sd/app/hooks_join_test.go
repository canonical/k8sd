package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	etcdclient "github.com/canonical/k8sd/pkg/client/etcd"
	etcdmock "github.com/canonical/k8sd/pkg/client/etcd/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/lxd/shared/revert"
	. "github.com/onsi/gomega"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestRegisterEtcdMemberReverter_NotEnoughEndpoints tests that cleanup is skipped when <3 endpoints for a voting member.
func TestRegisterEtcdMemberReverter_NotEnoughEndpoints(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	etcdDir := filepath.Join(tmpDir, "etcd")
	g.Expect(os.MkdirAll(etcdDir, 0o755)).To(Succeed())

	testFile := filepath.Join(etcdDir, "member/snap/db")
	g.Expect(os.MkdirAll(filepath.Dir(testFile), 0o755)).To(Succeed())
	g.Expect(os.WriteFile(testFile, []byte("etcd data"), 0o644)).To(Succeed())

	// Only 2 endpoints - RegisterEtcdMemberReverter skips etcd operations when <3 for voting members
	endpoints := []string{"https://node1:2379", "https://node2:2379"}

	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			EtcdDir: etcdDir,
			// No EtcdClient needed - reverter won't call snap.EtcdClient with <3 endpoints
		},
	}
	reverter := revert.New()

	registerEtcdMemberReverter(mockSnap, "node2", endpoints, false, reverter)

	// Trigger reverter
	reverter.Fail()

	// Verify directory was NOT cleaned up (quorum protection)
	g.Expect(etcdDir).To(BeADirectory())
}

// TestRegisterEtcdMemberReverter_ClientCreationFailure tests error handling when EtcdClient creation fails.
func TestRegisterEtcdMemberReverter_ClientCreationFailure(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	etcdDir := filepath.Join(tmpDir, "etcd")
	g.Expect(os.MkdirAll(etcdDir, 0o755)).To(Succeed())

	testFile := filepath.Join(etcdDir, "member/snap/db")
	g.Expect(os.MkdirAll(filepath.Dir(testFile), 0o755)).To(Succeed())
	g.Expect(os.WriteFile(testFile, []byte("etcd data"), 0o644)).To(Succeed())
	// 3 endpoints - should attempt etcd operations but client creation fails
	endpoints := []string{"https://node1:2379", "https://node2:2379", "https://node3:2379"}
	nodeName := "node2"

	// Mock snap that returns an error from EtcdClient
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			EtcdDir:       etcdDir,
			EtcdClientErr: fmt.Errorf("failed to create etcd client"),
		},
	}
	reverter := revert.New()

	registerEtcdMemberReverter(mockSnap, nodeName, endpoints, false, reverter)

	// Trigger reverter
	reverter.Fail()

	// Verify directory was NOT cleaned up when client creation fails
	g.Expect(etcdDir).To(BeADirectory(), "etcd directory should NOT be removed when client creation fails")
}

// TestRegisterEtcdMemberReverter_Learner_NoEndpoints tests that cleanup is skipped
// for a learner when no peer endpoints are available.
func TestRegisterEtcdMemberReverter_Learner_NoEndpoints(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	etcdDir := filepath.Join(tmpDir, "etcd")
	g.Expect(os.MkdirAll(etcdDir, 0o755)).To(Succeed())

	testFile := filepath.Join(etcdDir, "member/snap/db")
	g.Expect(os.MkdirAll(filepath.Dir(testFile), 0o755)).To(Succeed())
	g.Expect(os.WriteFile(testFile, []byte("etcd data"), 0o644)).To(Succeed())

	// No endpoints available - nothing to connect to
	endpoints := []string{}

	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			EtcdDir: etcdDir,
		},
	}
	reverter := revert.New()

	registerEtcdMemberReverter(mockSnap, "node2", endpoints, true, reverter)

	reverter.Fail()

	// Verify directory was NOT cleaned up (no endpoints to reach the cluster)
	g.Expect(etcdDir).To(BeADirectory(), "etcd directory should NOT be removed when no endpoints are available")
}

// TestRegisterEtcdMemberReverter_Learner_WithEndpoints_ClientError tests that
// for a learner, removal is attempted even with a single endpoint (no quorum
// guard), and handles client creation errors gracefully.
func TestRegisterEtcdMemberReverter_Learner_WithEndpoints_ClientError(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	etcdDir := filepath.Join(tmpDir, "etcd")
	g.Expect(os.MkdirAll(etcdDir, 0o755)).To(Succeed())

	testFile := filepath.Join(etcdDir, "member/snap/db")
	g.Expect(os.MkdirAll(filepath.Dir(testFile), 0o755)).To(Succeed())
	g.Expect(os.WriteFile(testFile, []byte("etcd data"), 0o644)).To(Succeed())

	// Single endpoint - sufficient for a learner (no quorum impact)
	endpoints := []string{"https://node1:2379"}
	nodeName := "node2"

	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			EtcdDir:       etcdDir,
			EtcdClientErr: fmt.Errorf("failed to create etcd client"),
		},
	}
	reverter := revert.New()

	registerEtcdMemberReverter(mockSnap, nodeName, endpoints, true, reverter)

	// Should not panic; errors are logged but do not propagate
	reverter.Fail()

	// Directory is not removed because client creation failed, but the code
	// must have attempted to create the client (isLearner=true bypasses the
	// endpoint count guard).
	g.Expect(etcdDir).To(BeADirectory(), "etcd directory should NOT be removed when client creation fails")
}

// TestRegisterEtcdLearnerAddReverter tests the cleanup of a learner that
// MemberAddAsLearner may have added although no attempt returned it.
func TestRegisterEtcdLearnerAddReverter(t *testing.T) {
	const peerURL = "https://10.0.0.2:2380"
	endpoints := []string{"https://10.0.0.1:2379"}
	voter := func() *etcdserverpb.Member {
		return &etcdserverpb.Member{ID: 1, Name: "cp1", PeerURLs: []string{"https://10.0.0.1:2380"}}
	}
	orphan := func() *etcdserverpb.Member {
		return &etcdserverpb.Member{ID: 7, PeerURLs: []string{peerURL}, IsLearner: true}
	}
	run := func(cluster *etcdmock.Cluster, add *etcdLearnerAdd) {
		mockSnap := &snapmock.Snap{Mock: snapmock.Mock{EtcdClient: etcdmock.NewClient(cluster)}}
		reverter := revert.New()
		registerEtcdLearnerAddReverter(mockSnap, endpoints, peerURL, add, reverter)
		reverter.Fail()
	}

	t.Run("AmbiguousAddCommitted", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{voter(), orphan()}}

		run(cluster, &etcdLearnerAdd{ambiguous: true})

		g.Expect(cluster.Member(peerURL)).To(BeNil())
		g.Expect(cluster.Members).To(HaveLen(1))
	})

	t.Run("AmbiguousAddCommittedLate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		// Not visible right after the error; it commits a few listings later.
		cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{voter()}, Late: []*etcdserverpb.Member{orphan()}, LateAfterLists: 3}

		run(cluster, &etcdLearnerAdd{ambiguous: true})

		g.Expect(cluster.Member(peerURL)).To(BeNil())
		g.Expect(cluster.Members).To(HaveLen(1))
	})

	t.Run("NeverRemovesVoter", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		joiner := &etcdserverpb.Member{ID: 7, Name: "cp2", PeerURLs: []string{peerURL}}
		cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{voter(), joiner}}

		run(cluster, &etcdLearnerAdd{ambiguous: true})

		g.Expect(cluster.Members).To(ContainElement(joiner))
		g.Expect(cluster.RemovedIDs).To(BeEmpty())
	})

	t.Run("RejectedAddRemovesStaleLearner", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		// The add was rejected because a learner that started before holds
		// the peer URL; it is not adopted, so the failed join removes it.
		stale := &etcdserverpb.Member{ID: 7, Name: "cp2", PeerURLs: []string{peerURL}, IsLearner: true}
		cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{voter(), stale}}

		run(cluster, &etcdLearnerAdd{})

		g.Expect(cluster.Member(peerURL)).To(BeNil())
	})

	t.Run("KnownLearnerLeftToEtcdReverter", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		learner := orphan()
		cluster := &etcdmock.Cluster{Members: []*etcdserverpb.Member{voter(), learner}}

		run(cluster, &etcdLearnerAdd{result: &etcdclient.AddLearnerResult{Member: learner}})

		g.Expect(cluster.Members).To(ContainElement(learner))
		g.Expect(cluster.Barriers).To(BeZero())
	})
}

// TestRegisterK8sNodeDeletionReverter_FailDeletesNode ensures a failed join triggers Node deletion.
func TestRegisterK8sNodeDeletionReverter_FailDeletesNode(t *testing.T) {
	g := NewWithT(t)

	nodeName := "test-node"
	clientset := fake.NewClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
	})
	k8sClient := &kubernetes.Client{Interface: clientset}

	reverter := revert.New()
	registerK8sNodeDeletionReverter(k8sClient, nodeName, reverter)

	// Simulate join failure
	reverter.Fail()

	// Node should be deleted
	_, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "node should be removed on revert")
}

// TestRegisterK8sNodeDeletionReverter_Success ensures a successful join does not delete the Node.
func TestRegisterK8sNodeDeletionReverter_Success(t *testing.T) {
	g := NewWithT(t)

	nodeName := "test-node"
	clientset := fake.NewClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
	})
	k8sClient := &kubernetes.Client{Interface: clientset}

	reverter := revert.New()
	defer reverter.Fail()

	registerK8sNodeDeletionReverter(k8sClient, nodeName, reverter)

	// Mark as successful join (reverts should not run)
	reverter.Success()

	// Node should still exist
	_, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred(), "node should remain when join succeeds")
}

func TestBuildInitialClusterMembers(t *testing.T) {
	tests := []struct {
		name     string
		members  []*etcdserverpb.Member
		expected map[string]string
	}{
		{
			name:     "nil members returns empty map",
			members:  nil,
			expected: map[string]string{},
		},
		{
			name:     "empty members returns empty map",
			members:  []*etcdserverpb.Member{},
			expected: map[string]string{},
		},
		{
			name: "single started voting member is included",
			members: []*etcdserverpb.Member{
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380"}, IsLearner: false},
			},
			expected: map[string]string{
				"node1": "https://10.0.0.1:2380",
			},
		},
		{
			name: "started learner member is included",
			members: []*etcdserverpb.Member{
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380"}, IsLearner: false},
				{ID: 2, Name: "learner1", PeerURLs: []string{"https://10.0.0.2:2380"}, IsLearner: true},
			},
			expected: map[string]string{
				"node1":    "https://10.0.0.1:2380",
				"learner1": "https://10.0.0.2:2380",
			},
		},
		{
			name: "unstarted member with no name is excluded",
			members: []*etcdserverpb.Member{
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380"}, IsLearner: false},
				{ID: 2, Name: "", PeerURLs: []string{"https://10.0.0.2:2380"}, IsLearner: true},
			},
			expected: map[string]string{
				"node1": "https://10.0.0.1:2380",
			},
		},
		{
			name: "member with no peer URLs is excluded",
			members: []*etcdserverpb.Member{
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380"}, IsLearner: false},
				{ID: 2, Name: "node2", PeerURLs: nil, IsLearner: false},
			},
			expected: map[string]string{
				"node1": "https://10.0.0.1:2380",
			},
		},
		{
			name: "member with empty peer URLs is excluded",
			members: []*etcdserverpb.Member{
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380"}, IsLearner: false},
				{ID: 2, Name: "node2", PeerURLs: []string{}, IsLearner: false},
			},
			expected: map[string]string{
				"node1": "https://10.0.0.1:2380",
			},
		},
		{
			name: "typical learner join: existing leader + unstarted learner self",
			members: []*etcdserverpb.Member{
				// Existing leader (voting member, started)
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380"}, IsLearner: false},
				// Newly added learner (self, not started yet - no name)
				{ID: 2, Name: "", PeerURLs: nil, IsLearner: true},
			},
			expected: map[string]string{
				"node1": "https://10.0.0.1:2380",
			},
		},
		{
			name: "three node cluster with one learner joining",
			members: []*etcdserverpb.Member{
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380"}, IsLearner: false},
				{ID: 2, Name: "node2", PeerURLs: []string{"https://10.0.0.2:2380"}, IsLearner: false},
				{ID: 3, Name: "node3", PeerURLs: []string{"https://10.0.0.3:2380"}, IsLearner: false},
				// Newly added learner (self, not started)
				{ID: 4, Name: "", PeerURLs: nil, IsLearner: true},
			},
			expected: map[string]string{
				"node1": "https://10.0.0.1:2380",
				"node2": "https://10.0.0.2:2380",
				"node3": "https://10.0.0.3:2380",
			},
		},
		{
			name: "uses first peer URL when multiple are present",
			members: []*etcdserverpb.Member{
				{ID: 1, Name: "node1", PeerURLs: []string{"https://10.0.0.1:2380", "https://10.0.0.1:2381"}, IsLearner: false},
			},
			expected: map[string]string{
				"node1": "https://10.0.0.1:2380",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := buildInitialClusterMembers(tt.members)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
