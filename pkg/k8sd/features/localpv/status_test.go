package localpv_test

import (
	"context"
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/localpv"
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
	nameLabelKey        = "app.kubernetes.io/name"
	nameLabelValue      = "rawfile-csi"
	componentLabelKey   = "component"
	controllerComponent = "controller"
	nodeComponent       = "node"
)

func componentLabels(component string) map[string]string {
	return map[string]string{
		nameLabelKey:      nameLabelValue,
		componentLabelKey: component,
	}
}

func readyPod(name string, labels map[string]string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels:    labels,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func failingPod(name string, labels map[string]string, reason string, restarts int32) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels:    labels,
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

func TestCheckLocalStorage(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("rawfile-csi-controller-0", componentLabels(controllerComponent)),
			readyPod("rawfile-csi-node-aaa", componentLabels(nodeComponent)),
			readyPod("rawfile-csi-node-bbb", componentLabels(nodeComponent)),
		)

		got := localpv.CheckLocalStorage(context.Background(), sn)

		g.Expect(got).To(Equal(types.ProbeResult{}))
	})

	t.Run("failed", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("rawfile-csi-controller-0", componentLabels(controllerComponent)),
			readyPod("rawfile-csi-node-aaa", componentLabels(nodeComponent)),
			failingPod("rawfile-csi-node-bbb", componentLabels(nodeComponent), "CrashLoopBackOff", 7),
			failingPod("rawfile-csi-node-ccc", componentLabels(nodeComponent), "CrashLoopBackOff", 3),
		)

		got := localpv.CheckLocalStorage(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateFailed))
		g.Expect(got.Message).To(Equal("CrashLoopBackOff on rawfile-csi-node (2/3 pods, 7 restarts max)"))
	})

	t.Run("controller failing", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			failingPod("rawfile-csi-controller-0", componentLabels(controllerComponent), "ErrImagePull", 4),
			readyPod("rawfile-csi-node-aaa", componentLabels(nodeComponent)),
		)

		got := localpv.CheckLocalStorage(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateFailed))
		g.Expect(got.Message).To(Equal("ErrImagePull on rawfile-csi-controller (1/1 pods, 4 restarts max)"))
	})

	t.Run("waiting", func(t *testing.T) {
		g := NewWithT(t)
		sn := snapWithPods(
			readyPod("rawfile-csi-controller-0", componentLabels(controllerComponent)),
		)

		got := localpv.CheckLocalStorage(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateWaiting))
		g.Expect(got.Message).To(Equal("Waiting for rawfile-csi-node pods to be scheduled. Check cluster events for details: k8s kubectl get events -n kube-system"))
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

		got := localpv.CheckLocalStorage(context.Background(), sn)

		g.Expect(got.State).To(Equal(apiv2.FeatureStateDegraded))
		g.Expect(got.Message).To(ContainSubstring("Could not verify local-storage pod health"))
		g.Expect(got.Err).To(HaveOccurred())
	})
}
