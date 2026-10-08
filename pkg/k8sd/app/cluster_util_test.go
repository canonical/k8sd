package app

import (
	"context"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// A node registering after a transient not-found get unblocks the wait, even
// though it is never Ready (e.g. managed networking disabled).
func TestWaitNodeRegistered_WaitsForNodeToRegister(t *testing.T) {
	g := NewWithT(t)

	clientset := fake.NewClientset()
	calls := 0
	clientset.PrependReactor("get", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		calls++
		if calls < 2 {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, "bootstrap-node")
		}
		return true, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-node"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			}},
		}, nil
	})
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{KubernetesClient: &kubernetes.Client{Interface: clientset}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	g.Expect(waitNodeRegistered(ctx, mockSnap, "bootstrap-node")).To(Succeed())
	g.Expect(calls).To(BeNumerically(">=", 2))
}

// The wait gives up once the context deadline passes if the node never registers.
func TestWaitNodeRegistered_TimesOutWhenNeverRegistered(t *testing.T) {
	g := NewWithT(t)

	clientset := fake.NewClientset()
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{KubernetesClient: &kubernetes.Client{Interface: clientset}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	g.Expect(waitNodeRegistered(ctx, mockSnap, "bootstrap-node")).NotTo(Succeed())
}
