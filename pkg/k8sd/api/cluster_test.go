package api

import (
	"context"
	"testing"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
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

func TestClusterIsReadyNetworkGate(t *testing.T) {
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

			objects := []runtime.Object{readyNode()}
			if tc.ciliumRunning {
				objects = append(objects,
					readyPod("cilium-operator", map[string]string{"io.cilium/app": "operator"}),
					readyPod("cilium", map[string]string{"k8s-app": "cilium"}),
				)
			}
			client := &kubernetes.Client{Interface: fake.NewSimpleClientset(objects...)}

			e := &Endpoints{
				provider: &snapmock.Provider{
					SnapFn: func() snap.Snap {
						return &snapmock.Snap{Mock: snapmock.Mock{KubernetesClient: client}}
					},
				},
			}

			ready, err := e.clusterIsReady(context.Background(), types.ClusterConfig{
				Network: types.Network{Enabled: utils.Pointer(tc.networkEnabled)},
			}, client)

			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(ready).To(Equal(tc.expectReady))
		})
	}
}
