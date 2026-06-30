package coredns_test

import (
	"context"
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/coredns"
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
	dnsLabelKey   = "app.kubernetes.io/name"
	dnsLabelValue = "coredns"
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

func TestCheckDNS(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("coredns-aaa", dnsLabelKey, dnsLabelValue),
			readyPod("coredns-bbb", dnsLabelKey, dnsLabelValue),
		)

		got := coredns.CheckDNS(context.Background(), sn)

		g.Expect(got).To(Equal(types.ProbeResult{}))
	})

	t.Run("failed", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("coredns-aaa", dnsLabelKey, dnsLabelValue),
			failingPod("coredns-bbb", dnsLabelKey, dnsLabelValue, "CrashLoopBackOff", 7),
			failingPod("coredns-ccc", dnsLabelKey, dnsLabelValue, "CrashLoopBackOff", 3),
		)

		got := coredns.CheckDNS(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateFailed))
		g.Expect(got.Message).To(Equal("CrashLoopBackOff on coredns (2/3 pods, 7 restarts max)"))
	})

	t.Run("waiting", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods()

		got := coredns.CheckDNS(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateWaiting))
		g.Expect(got.Message).To(Equal("Waiting for coredns pods to be scheduled. Check cluster events for details: k8s kubectl get events -n kube-system"))
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

		got := coredns.CheckDNS(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateDegraded))
		g.Expect(got.Message).To(ContainSubstring("Could not verify dns pod health"))
		g.Expect(got.Err).To(HaveOccurred())
	})
}
