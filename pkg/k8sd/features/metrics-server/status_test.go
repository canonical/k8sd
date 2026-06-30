package metrics_server_test

import (
	"context"
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	metrics_server "github.com/canonical/k8sd/pkg/k8sd/features/metrics-server"
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
	msLabelKey   = "app.kubernetes.io/name"
	msLabelValue = "metrics-server"
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

func TestCheckMetricsServer(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("metrics-server-abc", msLabelKey, msLabelValue),
		)

		got := metrics_server.CheckMetricsServer(context.Background(), sn)

		g.Expect(got).To(Equal(types.ProbeResult{}))
	})

	t.Run("failed", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("metrics-server-aaa", msLabelKey, msLabelValue),
			failingPod("metrics-server-bbb", msLabelKey, msLabelValue, "CrashLoopBackOff", 7),
		)

		got := metrics_server.CheckMetricsServer(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateFailed))
		g.Expect(got.Message).To(Equal("CrashLoopBackOff on metrics-server (1/2 pods, 7 restarts max)"))
	})

	t.Run("waiting", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods()

		got := metrics_server.CheckMetricsServer(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateWaiting))
		g.Expect(got.Message).To(Equal("Waiting for metrics-server pods to be scheduled. Check cluster events for details: k8s kubectl get events -n kube-system"))
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

		got := metrics_server.CheckMetricsServer(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateDegraded))
		g.Expect(got.Message).To(ContainSubstring("Could not verify metrics-server pod health"))
		g.Expect(got.Err).To(HaveOccurred())
	})
}
