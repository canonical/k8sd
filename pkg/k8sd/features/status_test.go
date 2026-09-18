package features_test

import (
	"context"
	"testing"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func operatorPod(ready bool) corev1.Pod {
	status := corev1.ConditionFalse
	phase := corev1.PodRunning
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium-operator",
			Namespace: "kube-system",
			Labels:    map[string]string{"io.cilium/app": "operator"},
		},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

func agentPod(ready bool) corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cilium",
			Namespace: "kube-system",
			Labels:    map[string]string{"k8s-app": "cilium"},
		},
		Status: corev1.PodStatus{
			// #1789: cilium-agent is Running but not Ready while crash-looping.
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

func TestCheckClusterReady(t *testing.T) {
	networkEnabled := types.ClusterConfig{Network: types.Network{Enabled: utils.Pointer(true)}}
	networkDisabled := types.ClusterConfig{Network: types.Network{Enabled: utils.Pointer(false)}}

	t.Run("ciliumAgentCrashLooping", func(t *testing.T) {
		g := NewWithT(t)

		clientset := fake.NewSimpleClientset(&corev1.PodList{
			Items: []corev1.Pod{operatorPod(true), agentPod(false)},
		})
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		err := features.StatusChecks.CheckClusterReady(context.Background(), snapM, networkEnabled)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("network is not ready"))
	})

	t.Run("noCiliumPods", func(t *testing.T) {
		g := NewWithT(t)

		clientset := fake.NewSimpleClientset()
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		err := features.StatusChecks.CheckClusterReady(context.Background(), snapM, networkEnabled)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("network is not ready"))
	})

	t.Run("ciliumOperatorDown", func(t *testing.T) {
		g := NewWithT(t)

		clientset := fake.NewSimpleClientset(&corev1.PodList{
			Items: []corev1.Pod{agentPod(true)},
		})
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		err := features.StatusChecks.CheckClusterReady(context.Background(), snapM, networkEnabled)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("network is not ready"))
	})

	t.Run("allReady", func(t *testing.T) {
		g := NewWithT(t)

		clientset := fake.NewSimpleClientset(&corev1.PodList{
			Items: []corev1.Pod{operatorPod(true), agentPod(true)},
		})
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		err := features.StatusChecks.CheckClusterReady(context.Background(), snapM, networkEnabled)

		g.Expect(err).NotTo(HaveOccurred())
	})

	t.Run("networkDisabled", func(t *testing.T) {
		g := NewWithT(t)

		clientset := fake.NewSimpleClientset()
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		err := features.StatusChecks.CheckClusterReady(context.Background(), snapM, networkDisabled)

		g.Expect(err).NotTo(HaveOccurred())
	})
}
