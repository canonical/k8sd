package app

import (
	"context"
	"testing"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/lxd/shared/revert"
	. "github.com/onsi/gomega"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

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

// fakeEtcdCluster is a test double for etcdCluster.
type fakeEtcdCluster struct {
	promoteErr error
	members    []*etcdserverpb.Member
	listErr    error
}

func (f *fakeEtcdCluster) MemberPromote(_ context.Context, _ uint64) (*clientv3.MemberPromoteResponse, error) {
	return nil, f.promoteErr
}

func (f *fakeEtcdCluster) MemberList(_ context.Context) (*clientv3.MemberListResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &clientv3.MemberListResponse{
		Members: f.members,
	}, nil
}

func TestPromoteEtcdLearnerIdempotent(t *testing.T) {
	const learnerID = uint64(42)

	tests := []struct {
		name       string
		cluster    *fakeEtcdCluster
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:    "succeeds on first promote call",
			cluster: &fakeEtcdCluster{promoteErr: nil},
			wantErr: false,
		},
		{
			name: "succeeds when member is already a voter (idempotent)",
			cluster: &fakeEtcdCluster{
				promoteErr: rpctypes.ErrMemberNotLearner,
				members:    []*etcdserverpb.Member{{ID: learnerID, Name: "node1", IsLearner: false}},
			},
			wantErr: false,
		},
		{
			name: "fails when ErrMemberNotLearner but member not found in list",
			cluster: &fakeEtcdCluster{
				promoteErr: rpctypes.ErrMemberNotLearner,
				members:    []*etcdserverpb.Member{},
			},
			wantErr:    true,
			wantErrMsg: "failed to promote etcd learner",
		},
		{
			name: "fails when ErrMemberNotLearner and MemberList also errors",
			cluster: &fakeEtcdCluster{
				promoteErr: rpctypes.ErrMemberNotLearner,
				listErr:    fmt.Errorf("list failed"),
			},
			wantErr:    true,
			wantErrMsg: "failed to promote etcd learner",
		},
		{
			name:       "fails on unrelated promote error",
			cluster:    &fakeEtcdCluster{promoteErr: fmt.Errorf("some other error")},
			wantErr:    true,
			wantErrMsg: "failed to promote etcd learner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := promoteEtcdLearnerIdempotent(ctx, tt.cluster, learnerID, "test-node")
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.wantErrMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.wantErrMsg))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
