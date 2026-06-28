package cilium_test

import (
	"context"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	helmmock "github.com/canonical/k8sd/pkg/client/helm/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/cilium"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCheckNetwork(t *testing.T) {
	t.Run("ciliumOperatorNotReady", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{
			ApplyChanged: true,
		}
		clientset := fake.NewSimpleClientset()
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
				KubernetesClient: &kubernetes.Client{
					Interface: clientset,
				},
			},
		}

		got := cilium.CheckNetwork(context.Background(), snapM)

		g.Expect(got.Err).To(HaveOccurred())
		g.Expect(got.State).To(Equal(apiv2.FeatureStateDegraded))
		g.Expect(got.Message).NotTo(BeEmpty())
	})

	t.Run("operatorNoCiliumPods", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{
			ApplyChanged: true,
		}
		clientset := fake.NewSimpleClientset(&corev1.PodList{
			Items: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod1",
						Namespace: "kube-system",
						Labels:    map[string]string{"io.cilium/app": "operator"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{Type: corev1.PodReady, Status: corev1.ConditionTrue},
						},
					},
				},
			},
		})
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
				KubernetesClient: &kubernetes.Client{
					Interface: clientset,
				},
			},
		}

		got := cilium.CheckNetwork(context.Background(), snapM)

		g.Expect(got.Err).To(HaveOccurred())
		g.Expect(got.State).To(Equal(apiv2.FeatureStateDegraded))
		g.Expect(got.Message).NotTo(BeEmpty())
	})

	t.Run("allPodsPresent", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{
			ApplyChanged: true,
		}
		clientset := fake.NewSimpleClientset(&corev1.PodList{
			Items: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator",
						Namespace: "kube-system",
						Labels:    map[string]string{"io.cilium/app": "operator"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{Type: corev1.PodReady, Status: corev1.ConditionTrue},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cilium",
						Namespace: "kube-system",
						Labels:    map[string]string{"k8s-app": "cilium"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						Conditions: []corev1.PodCondition{
							{Type: corev1.PodReady, Status: corev1.ConditionTrue},
						},
					},
				},
			},
		})
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
				KubernetesClient: &kubernetes.Client{
					Interface: clientset,
				},
			},
		}

		got := cilium.CheckNetwork(context.Background(), snapM)
		g.Expect(got).To(Equal(types.ProbeResult{}))
	})
}

// TestCheckStubsReturnZeroProbeResult is a smoke test that the cilium
// Check* stubs introduced in phase 1 return an empty ProbeResult, i.e.
// they explicitly opt out of overlaying anything onto the DB-derived
// FeatureStatus. Real probes for ingress/gateway/load-balancer will
// replace these in a follow-up.
func TestCheckStubsReturnZeroProbeResult(t *testing.T) {
	g := NewWithT(t)
	snapM := &snapmock.Snap{}
	ctx := context.Background()

	g.Expect(cilium.CheckIngress(ctx, snapM)).To(Equal(types.ProbeResult{}))
	g.Expect(cilium.CheckGateway(ctx, snapM)).To(Equal(types.ProbeResult{}))
	g.Expect(cilium.CheckLoadBalancer(ctx, snapM)).To(Equal(types.ProbeResult{}))
}
