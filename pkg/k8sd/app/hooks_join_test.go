package app

import (
	"context"
	"sync/atomic"
	"testing"

	upgradesv1alpha "github.com/canonical/k8s-snap-api/v2/api/v1alpha"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	upgradepkg "github.com/canonical/k8sd/pkg/upgrade"
	"github.com/canonical/k8sd/pkg/version"
	"github.com/canonical/lxd/shared/revert"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/client-go/kubernetes/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

type mockStateForUpgradeTest struct {
	mctypes.State
	name string
}

func (m *mockStateForUpgradeTest) Name() string {
	return m.name
}

func TestInitiateRollingUpgrade_ExistingUpgradeCR(t *testing.T) {
	g := NewWithT(t)
	scheme, err := kubernetes.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	thisVer := versionutil.MustParse("1.36.0")
	clusterVer := versionutil.MustParse("1.35.0")
	versionData := version.Info{Revision: "100", KubernetesVersion: thisVer}
	upgradeName := upgradepkg.GetName(versionData)

	existing := &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: upgradeName,
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase:         upgradesv1alpha.UpgradePhaseCompleted,
			Strategy:      upgradesv1alpha.UpgradeStrategyRollingUpgrade,
			UpgradedNodes: []string{"node1", "node2"},
		},
	}

	fakeClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).WithStatusSubresource(existing).Build()
	k8sClient := &kubernetes.Client{
		Client: fakeClient,
	}

	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			Revision: "100",
		},
	}

	state := &mockStateForUpgradeTest{name: "node1"}
	err = initiateRollingUpgrade(ctx, mockSnap, state, k8sClient, thisVer, clusterVer)
	g.Expect(err).NotTo(HaveOccurred())

	var updated upgradesv1alpha.Upgrade
	err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: upgradeName}, &updated)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(updated.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseNodeUpgrade))
	g.Expect(updated.Status.Strategy).To(Equal(upgradesv1alpha.UpgradeStrategyRollingUpgrade))
	g.Expect(updated.Status.UpgradedNodes).To(Equal([]string{"node1"}))
}

func TestInitiateRollingUpgrade_ConcurrentInProgress(t *testing.T) {
	g := NewWithT(t)
	scheme, err := kubernetes.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	thisVer := versionutil.MustParse("1.36.0")
	clusterVer := versionutil.MustParse("1.35.0")
	versionData := version.Info{Revision: "100", KubernetesVersion: thisVer}
	upgradeName := upgradepkg.GetName(versionData)

	existing := &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: upgradeName,
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase:         upgradesv1alpha.UpgradePhaseNodeUpgrade,
			Strategy:      upgradesv1alpha.UpgradeStrategyRollingUpgrade,
			UpgradedNodes: []string{"node1"},
		},
	}

	fakeClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).WithStatusSubresource(existing).Build()
	k8sClient := &kubernetes.Client{
		Client: fakeClient,
	}

	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			Revision: "100",
		},
	}

	state := &mockStateForUpgradeTest{name: "node2"}
	err = initiateRollingUpgrade(ctx, mockSnap, state, k8sClient, thisVer, clusterVer)
	g.Expect(err).NotTo(HaveOccurred())

	var updated upgradesv1alpha.Upgrade
	err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: upgradeName}, &updated)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(updated.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseNodeUpgrade))
	g.Expect(updated.Status.UpgradedNodes).To(ConsistOf("node1", "node2"))
}

func TestInitiateRollingUpgrade_ConcurrentConflict(t *testing.T) {
	g := NewWithT(t)
	scheme, err := kubernetes.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	thisVer := versionutil.MustParse("1.36.0")
	clusterVer := versionutil.MustParse("1.35.0")
	versionData := version.Info{Revision: "100", KubernetesVersion: thisVer}
	upgradeName := upgradepkg.GetName(versionData)

	existing := &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: upgradeName,
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase:         upgradesv1alpha.UpgradePhaseCompleted,
			Strategy:      upgradesv1alpha.UpgradeStrategyRollingUpgrade,
			UpgradedNodes: []string{"node-old"},
		},
	}

	var patchAttempts atomic.Int32
	interceptorFuncs := interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, client ctrlclient.Client, subResourceName string, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
			if subResourceName == "status" && patchAttempts.Add(1) == 1 {
				var current upgradesv1alpha.Upgrade
				if err := client.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), &current); err == nil {
					current.Status.Phase = upgradesv1alpha.UpgradePhaseFeatureUpgrade
					current.Status.UpgradedNodes = []string{"node1"}
					_ = client.Status().Update(ctx, &current)
				}
			}
			return client.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
	}

	fakeClient := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existing).
		WithStatusSubresource(existing).
		WithInterceptorFuncs(interceptorFuncs).
		Build()
	k8sClient := &kubernetes.Client{
		Client: fakeClient,
	}

	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			Revision: "100",
		},
	}

	state := &mockStateForUpgradeTest{name: "node2"}
	err = initiateRollingUpgrade(ctx, mockSnap, state, k8sClient, thisVer, clusterVer)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(patchAttempts.Load()).To(BeNumerically(">=", 2))

	var updated upgradesv1alpha.Upgrade
	err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: upgradeName}, &updated)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(updated.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseFeatureUpgrade))
	g.Expect(updated.Status.UpgradedNodes).To(ConsistOf("node1", "node2"))
}
