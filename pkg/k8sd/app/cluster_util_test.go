package app

import (
	"context"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// A node becoming Ready after a transient not-ready list unblocks the wait.
func TestWaitNodeReady_WaitsForNodeToBecomeReady(t *testing.T) {
	g := NewWithT(t)

	clientset := fake.NewClientset()
	calls := 0
	clientset.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		calls++
		ready := corev1.ConditionFalse
		if calls >= 2 {
			ready = corev1.ConditionTrue
		}
		return true, &corev1.NodeList{Items: []corev1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-node"},
				Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: ready},
				}},
			},
		}}, nil
	})
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{KubernetesClient: &kubernetes.Client{Interface: clientset}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	g.Expect(waitNodeReady(ctx, mockSnap)).To(Succeed())
	g.Expect(calls).To(BeNumerically(">=", 2))
}

// The wait gives up once the context deadline passes if the node never becomes Ready.
func TestWaitNodeReady_TimesOutWhenNeverReady(t *testing.T) {
	g := NewWithT(t)

	clientset := fake.NewClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-node"},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
		}},
	})
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{KubernetesClient: &kubernetes.Client{Interface: clientset}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	g.Expect(waitNodeReady(ctx, mockSnap)).NotTo(Succeed())
}
