package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/api"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
	testenv "github.com/canonical/k8sd/pkg/utils/microcluster"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func readyNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func readyPod(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kube-system", Labels: labels},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

// clusterStatusHandler returns the GET handler registered for the cluster status
// endpoint. The handler itself is unexported and this package cannot be an internal
// test of package api: testenv imports pkg/k8sd/app, which imports pkg/k8sd/api.
func clusterStatusHandler(t *testing.T, ctx context.Context, provider api.Provider) func(mctypes.State, *http.Request) mctypes.Response {
	t.Helper()

	for _, resource := range api.New(ctx, provider, 0)["k8sd"].Resources {
		for _, endpoint := range resource.Endpoints {
			if endpoint.Path == apiv2.ClusterStatusRPC {
				return endpoint.Get.Handler
			}
		}
	}
	t.Fatalf("no endpoint registered for %s", apiv2.ClusterStatusRPC)
	return nil
}

func TestGetClusterStatusNetworkGate(t *testing.T) {
	for _, tc := range []struct {
		name           string
		networkEnabled bool
		ciliumRunning  bool
		expectReady    bool
	}{
		{name: "networkEnabledCiliumReady", networkEnabled: true, ciliumRunning: true, expectReady: true},
		{name: "networkEnabledCiliumMissing", networkEnabled: true, ciliumRunning: false, expectReady: false},
		{name: "networkDisabledCiliumMissing", networkEnabled: false, ciliumRunning: false, expectReady: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			testenv.WithState(t, func(ctx context.Context, s mctypes.State) {
				err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
					_, err := database.SetClusterConfig(ctx, tx, types.ClusterConfig{
						Network: types.Network{
							Enabled: utils.Pointer(tc.networkEnabled),
							// MergeClusterConfig runs ClusterConfig.Validate, which rejects
							// empty pod/service CIDRs, so both must be set here.
							PodCIDR:     utils.Pointer("10.1.0.0/16"),
							ServiceCIDR: utils.Pointer("10.152.183.0/24"),
						},
					})
					return err
				})
				g.Expect(err).To(Not(HaveOccurred()))

				objects := []runtime.Object{readyNode()}
				if tc.ciliumRunning {
					objects = append(objects,
						readyPod("cilium-operator", map[string]string{"io.cilium/app": "operator"}),
						readyPod("cilium", map[string]string{"k8s-app": "cilium"}),
					)
				}
				clientset := fake.NewSimpleClientset(objects...)

				provider := &snapmock.Provider{
					SnapFn: func() snap.Snap {
						return &snapmock.Snap{Mock: snapmock.Mock{
							KubernetesClient: &kubernetes.Client{Interface: clientset},
							K8sdClient:       &k8sdmock.Mock{},
						}}
					},
				}

				req := (&http.Request{Header: make(http.Header)}).WithContext(mctypes.ContextWithLogger(ctx))
				w := httptest.NewRecorder()
				resp := clusterStatusHandler(t, ctx, provider)(s, req)
				g.Expect(resp.Render(w, req)).To(Not(HaveOccurred()))

				var body struct {
					Metadata apiv2.ClusterStatusResponse `json:"metadata"`
				}
				g.Expect(json.Unmarshal(w.Body.Bytes(), &body)).To(Not(HaveOccurred()))
				g.Expect(body.Metadata.ClusterStatus.Ready).To(Equal(tc.expectReady))
			})
		})
	}
}
