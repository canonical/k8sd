package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	// Tests that checkNodeNameAvailable returns BadRequest when a node with the given name already exists.
	g := NewWithT(t)
	shared_name := "duplicate-node"
	existingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: shared_name,
		},
	}
	fakeClientset := fake.NewSimpleClientset(existingNode)
	k8sClient := &kubernetes.Client{Interface: fakeClientset}

	resp := checkNodeNameAvailable(context.Background(), k8sClient, shared_name)
	g.Expect(resp).ToNot(BeNil())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	err := resp.Render(w, req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(w.Code).To(Equal(http.StatusBadRequest))

	var respBody map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &respBody)
	g.Expect(err).ToNot(HaveOccurred())

	expectedError := "a node with the same name \"" + shared_name + "\" is already part of the cluster"
	g.Expect(respBody["error"]).To(Equal(expectedError))
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
	resp := checkNodeNameAvailable(context.Background(), k8sClient, "unique-node-2")
	g.Expect(resp).To(BeNil())
}

func TestCheckNodeNameAvailable_API_Errors(t *testing.T) {
	// Tests that checkNodeNameAvailable returns InternalError when the K8s API throws a non-NotFound error.
	g := NewWithT(t)

	// Create fake K8s client and inject an error for GET nodes RPC.
	fakeClientset := fake.NewSimpleClientset()
	fakeClientset.PrependReactor("get", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		_, ok := action.(k8stesting.GetAction)
		if ok {
			return true, nil, apierrors.NewInternalError(fmt.Errorf("mock API error"))
		}
		return false, nil, nil
	})

	k8sClient := &kubernetes.Client{Interface: fakeClientset}

	resp := checkNodeNameAvailable(context.Background(), k8sClient, "any-node")
	g.Expect(resp).ToNot(BeNil())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	err := resp.Render(w, req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(w.Code).To(Equal(http.StatusInternalServerError))

	var respBody map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &respBody)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(respBody["error"]).To(ContainSubstring("failed to check whether node name is available \"any-node\":"))
	g.Expect(respBody["error"]).To(ContainSubstring("mock API error"))
}
