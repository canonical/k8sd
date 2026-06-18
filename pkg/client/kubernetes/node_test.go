package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestGetNodeName(t *testing.T) {
	g := NewGomegaWithT(t)

	testCases := []struct {
		name          string
		nodeName      string
		existingNodes []*corev1.Node // Use a slice of *corev1.Node to represent existing nodes
		expectedError string
	}{
		{
			name:          "node name is empty",
			nodeName:      "",
			existingNodes: []*corev1.Node{},
			expectedError: apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "nodes"}, "").Error(),
		},
		{
			name:          "node name does not exist",
			nodeName:      "new-node-name",
			existingNodes: []*corev1.Node{},
			expectedError: apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "nodes"}, "new-node-name").Error(),
		},
		{
			name:     "node name exists",
			nodeName: "existing-node-name",
			existingNodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "existing-node-name",
					},
				},
			},
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a slice of runtime.Object to hold the existing nodes
			var runtimeObjects []runtime.Object
			for _, node := range tc.existingNodes {
				runtimeObjects = append(runtimeObjects, node)
			}

			// Create a fake clientset and add the existing nodes
			clientset := fake.NewSimpleClientset(runtimeObjects...)

			// Create a new k8s client with the fake clientset
			client := &Client{
				Interface: clientset,
			}

			// Call the GetNode method
			n, err := client.GetNode(context.Background(), tc.nodeName)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(n).NotTo(BeNil())
				g.Expect(n.Name).To(Equal(tc.nodeName))
			}
		})
	}
}

func TestDeleteNode(t *testing.T) {
	g := NewWithT(t)

	t.Run("node deletion is successful", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		client := &Client{Interface: clientset}
		nodeName := "test-node"
		client.CoreV1().Nodes().Create(context.TODO(), &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}, metav1.CreateOptions{})

		err := client.DeleteNode(context.Background(), nodeName)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("node does not exist is successful", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		client := &Client{Interface: clientset}
		nodeName := "test-node"

		err := client.DeleteNode(context.Background(), nodeName)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("node deletion fails", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		client := &Client{Interface: clientset}
		nodeName := "test-node"
		expectedErr := errors.New("some error")
		clientset.PrependReactor("delete", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, expectedErr
		})

		err := client.DeleteNode(context.Background(), nodeName)
		g.Expect(err).To(MatchError(fmt.Errorf("failed to delete node: %w", expectedErr)))
	})

	t.Run("node deletion fails with internal server error", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		client := &Client{Interface: clientset}
		nodeName := "test-node"
		client.CoreV1().Nodes().Create(context.TODO(), &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}, metav1.CreateOptions{})

		expectedErr := apierrors.NewInternalError(errors.New("database is locked"))
		clientset.PrependReactor("delete", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, expectedErr
		})

		err := client.DeleteNode(context.Background(), nodeName)
		g.Expect(err).To(MatchError(fmt.Errorf("failed to delete node: %w", expectedErr)))
	})

	t.Run("node deletion succeeds with internal server error", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		client := &Client{Interface: clientset}
		nodeName := "test-node"
		client.CoreV1().Nodes().Create(context.TODO(), &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}, metav1.CreateOptions{})

		expectedErr := apierrors.NewInternalError(errors.New("database is locked"))
		tries := 0
		clientset.PrependReactor("delete", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			if tries == 3 {
				return true, nil, nil
			}
			tries++
			return true, nil, expectedErr
		})

		err := client.DeleteNode(context.Background(), nodeName)
		g.Expect(err).To(Not(HaveOccurred()))
	})
}

func TestNodeVersions(t *testing.T) {
	g := NewWithT(t)

	t.Run("returns versions for all nodes", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				Status: v1.NodeStatus{
					NodeInfo: v1.NodeSystemInfo{KubeletVersion: "v1.28.1"},
				},
			},
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node2"},
				Status: v1.NodeStatus{
					NodeInfo: v1.NodeSystemInfo{KubeletVersion: "v1.29.0"},
				},
			},
		)

		client := &Client{Interface: clientset}

		versions, err := client.NodeVersions(context.Background())
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(versions).To(HaveLen(2))

		v1, _ := versionutil.ParseGeneric("v1.28.1")
		v2, _ := versionutil.ParseGeneric("v1.29.0")

		g.Expect(versions["node1"].EqualTo(v1)).To(BeTrue())
		g.Expect(versions["node2"].EqualTo(v2)).To(BeTrue())
	})

	t.Run("returns error on invalid version", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-bad"},
				Status: v1.NodeStatus{
					NodeInfo: v1.NodeSystemInfo{KubeletVersion: "not-a-version"},
				},
			},
		)
		client := &Client{Interface: clientset}

		_, err := client.NodeVersions(context.Background())
		g.Expect(err).To(MatchError(ContainSubstring("failed to parse version")))
	})
}

func TestListNodesStatuses(t *testing.T) {
	g := NewWithT(t)

	node := func(name, internalIP string, controlPlane bool, ready v1.ConditionStatus) *v1.Node {
		labels := map[string]string{}
		if controlPlane {
			labels["node-role.kubernetes.io/control-plane"] = ""
		}
		return &v1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			Status: v1.NodeStatus{
				Addresses: []v1.NodeAddress{
					{Type: v1.NodeHostName, Address: name},
					{Type: v1.NodeInternalIP, Address: internalIP},
				},
				Conditions: []v1.NodeCondition{
					{Type: v1.NodeReady, Status: ready},
				},
			},
		}
	}

	t.Run("derives role, readiness, reachability and address", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(
			node("cp1", "10.0.0.1", true, v1.ConditionTrue),
			node("worker1", "10.0.0.2", false, v1.ConditionTrue),
			node("worker2", "10.0.0.3", false, v1.ConditionFalse),
		)
		client := &Client{Interface: clientset}

		statuses, err := client.ListNodesStatuses(context.Background())
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(statuses).To(HaveLen(3))

		byName := make(map[string]apiv2.NodeStatus, len(statuses))
		for _, s := range statuses {
			byName[s.Name] = s
		}

		g.Expect(byName["cp1"].ClusterRole).To(Equal(apiv2.ClusterRoleControlPlane))
		g.Expect(byName["cp1"].Address).To(Equal("10.0.0.1"))
		g.Expect(byName["cp1"].Ready).To(BeTrue())
		g.Expect(byName["cp1"].Reachable).To(BeTrue())

		g.Expect(byName["worker1"].ClusterRole).To(Equal(apiv2.ClusterRoleWorker))
		g.Expect(byName["worker1"].Address).To(Equal("10.0.0.2"))
		g.Expect(byName["worker1"].Ready).To(BeTrue())

		// NotReady node: not ready (and, per current placeholder logic, not reachable).
		g.Expect(byName["worker2"].ClusterRole).To(Equal(apiv2.ClusterRoleWorker))
		g.Expect(byName["worker2"].Ready).To(BeFalse())
		g.Expect(byName["worker2"].Reachable).To(BeFalse())
	})

	t.Run("returns empty for a cluster with no nodes", func(t *testing.T) {
		client := &Client{Interface: fake.NewSimpleClientset()}

		statuses, err := client.ListNodesStatuses(context.Background())
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(statuses).To(BeEmpty())
	})
}
