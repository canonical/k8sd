package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCheckNodeNameAvailable_Duplicate_Node(t *testing.T) {
	// Tests that checkNodeNameAvailable returns error when a node with the given name already exists.
	g := NewWithT(t)
	shared_name := "duplicate-node"
	existingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: shared_name,
		},
	}
	fakeClientset := fake.NewSimpleClientset(existingNode)
	k8sClient := &kubernetes.Client{Interface: fakeClientset}

	err := checkNodeNameAvailable(context.Background(), k8sClient, shared_name)
	g.Expect(err).To(HaveOccurred())

	expectedError := fmt.Sprintf(errNodeNameAlreadyExists, shared_name)
	g.Expect(err.Error()).To(Equal(expectedError))
}

func TestCheckNodeNameAvailable_Unique_Name(t *testing.T) {
	// Tests that checkNodeNameAvailable returns nil when the node name is unique.
	g := NewWithT(t)

	existingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "unique-node-1",
		},
	}
	fakeClientset := fake.NewSimpleClientset(existingNode)
	k8sClient := &kubernetes.Client{Interface: fakeClientset}

	// Check for a different node name
	err := checkNodeNameAvailable(context.Background(), k8sClient, "unique-node-2")
	g.Expect(err).ToNot(HaveOccurred())
}

func TestCheckNodeNameAvailable_API_Errors(t *testing.T) {
	// Tests that checkNodeNameAvailable returns error when the K8s API throws a non-NotFound error.
	g := NewWithT(t)

	// Create fake K8s client and inject an error for GET nodes RPC.
	fakeClientset := fake.NewSimpleClientset()
	mockError := apierrors.NewInternalError(fmt.Errorf("mock API error"))
	fakeClientset.PrependReactor("get", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		_, ok := action.(k8stesting.GetAction)
		if ok {
			return true, nil, mockError
		}
		return false, nil, nil
	})

	k8sClient := &kubernetes.Client{Interface: fakeClientset}

	failedNodeName := "any-node"
	err := checkNodeNameAvailable(context.Background(), k8sClient, failedNodeName)
	g.Expect(err).To(HaveOccurred())

	expectedPrefix := fmt.Sprintf(errFailedToCheckNodeName, failedNodeName)
	g.Expect(err.Error()).To(ContainSubstring(expectedPrefix))
	g.Expect(err.Error()).To(ContainSubstring(mockError.Error()))
}
