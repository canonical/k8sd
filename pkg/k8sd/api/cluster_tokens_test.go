package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apiv1 "github.com/canonical/k8s-snap-api/api/v1"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/snap"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/microcluster/v2/microcluster"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// MockProvider is a manual mock implementation of the Provider interface.
type MockProvider struct {
	mockSnap snap.Snap
}

func (m *MockProvider) MicroCluster() *microcluster.MicroCluster {
	return nil
}

func (m *MockProvider) Snap() snap.Snap {
	return m.mockSnap
}

func (m *MockProvider) NotifyUpdateNodeConfigController() {}

func (m *MockProvider) NotifyFeatureController(network, gateway, ingress, loadBalancer, localStorage, metricsServer, dns bool) {
}

func newEndpointsWithK8sClientHelper(startingNode *corev1.Node) *Endpoints {
	// Create mock Snap with a fake K8s client containing the specified starting node.
	mockSnap := &snapmock.Snap{}
	fakeClientset := fake.NewSimpleClientset(startingNode)
	k8sClient := &kubernetes.Client{
		Interface: fakeClientset,
	}
	mockSnap.Mock.KubernetesClient = k8sClient

	mockProvider := &MockProvider{mockSnap: mockSnap}

	return &Endpoints{
		context:  context.Background(),
		provider: mockProvider,
	}
}

func postJoinTokenRequest(t *testing.T, endpoints *Endpoints, reqBody apiv1.GetJoinTokenRequest) (*httptest.ResponseRecorder, []byte) {
	// Sends a POST request to create a join token with the specified request body, returning the response.
	g := NewWithT(t)

	bodyBytes, err := json.Marshal(reqBody)
	g.Expect(err).ToNot(HaveOccurred())

	req, err := http.NewRequest("POST", "/cluster/tokens", bytes.NewReader(bodyBytes))
	g.Expect(err).ToNot(HaveOccurred())

	resp := endpoints.postClusterJoinTokens(nil, req)

	w := httptest.NewRecorder()
	err = resp.Render(w, req)
	g.Expect(err).ToNot(HaveOccurred())

	return w, w.Body.Bytes()
}

func TestPostClusterJoinTokens_DuplicateNode(t *testing.T) {

	// This method tests that when a request is made to create a join token
	// with a node name that already exists in the cluster, an appropriate
	// error is returned.

	// Create fake K8s client with existing node
	g := NewWithT(t)
	shared_name := "duplicate-node"
	existingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: shared_name,
		},
	}
	endpoints := newEndpointsWithK8sClientHelper(existingNode)

	// Request with duplicate name
	reqBody := apiv1.GetJoinTokenRequest{
		Name: shared_name,
		TTL:  time.Hour,
	}
	w, respBytes := postJoinTokenRequest(t, endpoints, reqBody)
	g.Expect(w.Code).To(Equal(http.StatusBadRequest))

	var respBody map[string]interface{}
	err := json.Unmarshal(respBytes, &respBody)
	g.Expect(err).ToNot(HaveOccurred())

	expectedError := "a node with the same name \"" + shared_name + "\" is already part of the cluster"
	g.Expect(respBody["error"]).To(Equal(expectedError))
}

func TestPostClusterJoinTokens_Success_With_UniqueNames(t *testing.T) {
	//Tests that a join token is successfully created when a unique node name is provided.
	g := NewWithT(t)

	// Create fake K8s client without any existing nodes
	existingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "unique-node-1",
		},
	}
	endpoints := newEndpointsWithK8sClientHelper(existingNode)

	// Request with unique name
	reqBody := apiv1.GetJoinTokenRequest{
		Name: "unique-node-2",
		TTL:  time.Hour,
	}

	original := getOrCreateJoinTokenFn
	t.Cleanup(func() {
		getOrCreateJoinTokenFn = original
	})
	getOrCreateJoinTokenFn = func(ctx context.Context, m *microcluster.MicroCluster, tokenName string, ttl time.Duration) (string, error) {
		// In these unit tests, the Provider's MicroCluster is nil; we stub the join-token
		// creation to avoid spinning up a real microcluster instance.
		g.Expect(m).To(BeNil())
		g.Expect(tokenName).To(Equal(reqBody.Name))
		g.Expect(ttl).To(Equal(reqBody.TTL))
		return "dummy-token", nil
	}

	w, respBytes := postJoinTokenRequest(t, endpoints, reqBody)
	g.Expect(w.Code).To(Equal(http.StatusOK))

	// struct so we unmarshal just the relevant part of the response
	type syncResponseEnvelope struct {
		Metadata apiv1.GetJoinTokenResponse `json:"metadata"`
	}

	var respBody syncResponseEnvelope
	err := json.Unmarshal(respBytes, &respBody)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(respBody.Metadata.EncodedToken).To(Equal("dummy-token"))
}

func TestPostClusterJoinTokens_CatchAPIErrors(t *testing.T) {
	// This method tests that when the K8s client throws a
	// mock api error, we properly catch and return it.

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

	mockSnap := &snapmock.Snap{}
	mockSnap.Mock.KubernetesClient = k8sClient
	endpoints := &Endpoints{context: context.Background(), provider: &MockProvider{mockSnap: mockSnap}}

	// Request with any name
	reqBody := apiv1.GetJoinTokenRequest{
		Name: "any-node",
		TTL:  time.Hour,
	}
	w, respBytes := postJoinTokenRequest(t, endpoints, reqBody)
	g.Expect(w.Code).To(Equal(http.StatusInternalServerError))

	var respBody map[string]interface{}
	err := json.Unmarshal(respBytes, &respBody)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(respBody["error"]).To(ContainSubstring("failed to check whether node name is available \"any-node\":"))
	g.Expect(respBody["error"]).To(ContainSubstring("mock API error"))
}
