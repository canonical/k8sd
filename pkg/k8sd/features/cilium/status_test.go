package cilium_test

import (
	"context"
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/cilium"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	operatorLabelKey   = "io.cilium/app"
	operatorLabelValue = "operator"
	agentLabelKey      = "k8s-app"
	agentLabelValue    = "cilium"
)

func readyPod(name, labelKey, labelValue string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels:    map[string]string{labelKey: labelValue},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func failingPod(name, labelKey, labelValue, reason string, restarts int32) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels:    map[string]string{labelKey: labelValue},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "main",
					RestartCount: restarts,
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: reason},
					},
				},
			},
		},
	}
}

func snapWithPods(pods ...corev1.Pod) snap.Snap {
	objs := make([]runtime.Object, 0, len(pods))
	for i := range pods {
		objs = append(objs, &pods[i])
	}
	return &snapmock.Snap{
		Mock: snapmock.Mock{
			KubernetesClient: &kubernetes.Client{Interface: fake.NewSimpleClientset(objs...)},
		},
	}
}

func TestCheckNetwork(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("cilium-operator", operatorLabelKey, operatorLabelValue),
			readyPod("cilium-agent-abc", agentLabelKey, agentLabelValue),
		)

		got := cilium.CheckNetwork(context.Background(), sn)

		g.Expect(got).To(Equal(types.ProbeResult{}))
	})

	t.Run("failed", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("cilium-operator", operatorLabelKey, operatorLabelValue),
			readyPod("cilium-agent-aaa", agentLabelKey, agentLabelValue),
			readyPod("cilium-agent-bbb", agentLabelKey, agentLabelValue),
			failingPod("cilium-agent-ccc", agentLabelKey, agentLabelValue, "CrashLoopBackOff", 7),
			failingPod("cilium-agent-ddd", agentLabelKey, agentLabelValue, "CrashLoopBackOff", 3),
			failingPod("cilium-agent-eee", agentLabelKey, agentLabelValue, "CrashLoopBackOff", 5),
		)

		got := cilium.CheckNetwork(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateFailed))
		g.Expect(got.Message).To(Equal("CrashLoopBackOff on cilium-agent (3/5 pods, 7 restarts max)"))
	})

	t.Run("waiting", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("cilium-operator", operatorLabelKey, operatorLabelValue),
		)

		got := cilium.CheckNetwork(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateWaiting))
		g.Expect(got.Message).To(Equal("Waiting for cilium-agent pods to be scheduled. Check cluster events for details: k8s kubectl get events -n kube-system"))
	})

	t.Run("degraded", func(t *testing.T) {
		g := NewWithT(t)
		cs := fake.NewSimpleClientset()
		cs.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("boom")
		})
		sn := &snapmock.Snap{
			Mock: snapmock.Mock{
				KubernetesClient: &kubernetes.Client{Interface: cs},
			},
		}

		got := cilium.CheckNetwork(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateDegraded))
		g.Expect(got.Message).To(ContainSubstring("Could not verify cilium network pod health"))
		g.Expect(got.Err).To(HaveOccurred())
	})
}

// TestCheckCiliumFeatureProbes verifies that the ingress, gateway and
// load-balancer probes derive their health from the same cilium operator +
// agent workloads as the network probe, differing only in the Degraded
// message qualifier.
func TestCheckCiliumFeatureProbes(t *testing.T) {
	checks := map[string]struct {
		fn      func(context.Context, snap.Snap) types.ProbeResult
		feature string
	}{
		"ingress":      {fn: cilium.CheckIngress, feature: "ingress"},
		"gateway":      {fn: cilium.CheckGateway, feature: "gateway"},
		"loadbalancer": {fn: cilium.CheckLoadBalancer, feature: "load-balancer"},
	}

	for name, tc := range checks {
		t.Run(name+"/healthy", func(t *testing.T) {
			g := NewWithT(t)
			sn := snapWithPods(
				readyPod("cilium-operator", operatorLabelKey, operatorLabelValue),
				readyPod("cilium-agent-abc", agentLabelKey, agentLabelValue),
			)

			got := tc.fn(context.Background(), sn)

			g.Expect(got).To(Equal(types.ProbeResult{}))
		})

		t.Run(name+"/failed", func(t *testing.T) {
			g := NewWithT(t)
			sn := snapWithPods(
				readyPod("cilium-operator", operatorLabelKey, operatorLabelValue),
				readyPod("cilium-agent-aaa", agentLabelKey, agentLabelValue),
				failingPod("cilium-agent-bbb", agentLabelKey, agentLabelValue, "CrashLoopBackOff", 7),
			)

			got := tc.fn(context.Background(), sn)

			g.Expect(got.State).To(Equal(apiv2.FeatureStateFailed))
			g.Expect(got.Message).To(Equal("CrashLoopBackOff on cilium-agent (1/2 pods, 7 restarts max)"))
		})

		t.Run(name+"/degraded", func(t *testing.T) {
			g := NewWithT(t)
			cs := fake.NewSimpleClientset()
			cs.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("boom")
			})
			sn := &snapmock.Snap{
				Mock: snapmock.Mock{
					KubernetesClient: &kubernetes.Client{Interface: cs},
				},
			}

			got := tc.fn(context.Background(), sn)

			g.Expect(got.State).To(Equal(apiv2.FeatureStateDegraded))
			g.Expect(got.Message).To(ContainSubstring("Could not verify cilium " + tc.feature + " pod health"))
			g.Expect(got.Err).To(HaveOccurred())
		})
	}
}
