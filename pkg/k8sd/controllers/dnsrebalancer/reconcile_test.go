package dnsrebalancer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/coredns"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcile_LessThanTwoNodesReady(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	reconciler, k8sClient := newReconcileTestController(t, nil)
	g.Expect(reconciler.client.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}})).To(Succeed())

	result, err := reconciler.Reconcile(ctx, ctrl.Request{})

	g.Expect(result).To(Equal(ctrl.Result{}))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(deploymentUpdateCount(k8sClient)).To(BeZero())
}

func TestReconcile_CoreDNSAlreadyBalanced(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	reconciler, k8sClient := newReconcileTestController(t, func(_ *corev1.Node, pod *corev1.Pod, _ *appsv1.Deployment) {
		pod.Spec.NodeName = "node-2"
	})

	result, err := reconciler.Reconcile(ctx, ctrl.Request{})

	g.Expect(result).To(Equal(ctrl.Result{}))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(deploymentUpdateCount(k8sClient)).To(BeZero())
}

func TestCoreDNSNeedsRebalancing_AllPodsSameNode(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-1",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{NodeName: "node-a"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-2",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{NodeName: "node-a"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(&pods[0], &pods[1]).
		Build()

	reconciler := &controller{
		logger: ctrl.Log.WithName("test"),
		client: fakeClient,
		snap:   nil,
	}

	needsRebalancing, err := reconciler.coreDNSNeedsRebalancing(ctx)

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(needsRebalancing).To(BeTrue(), "should need rebalancing when all pods on same node")
}

func TestCoreDNSNeedsRebalancing_Distributed(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-1",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{NodeName: "node-a"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-2",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{NodeName: "node-b"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(&pods[0], &pods[1]).
		Build()

	reconciler := &controller{
		logger: ctrl.Log.WithName("test"),
		client: fakeClient,
		snap:   nil,
	}

	needsRebalancing, err := reconciler.coreDNSNeedsRebalancing(ctx)

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(needsRebalancing).To(BeFalse(), "should not need rebalancing when pods distributed")
}

func TestCoreDNSNeedsRebalancing_IgnoresUnscheduledPods(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// One scheduled, one pending (no NodeName)
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-1",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{NodeName: "node-a"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-2",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(&pods[0], &pods[1]).
		Build()

	reconciler := &controller{
		logger: ctrl.Log.WithName("test"),
		client: fakeClient,
		snap:   nil,
	}

	needsRebalancing, err := reconciler.coreDNSNeedsRebalancing(ctx)

	g.Expect(err.Error()).To(Equal("less than 2 pods are scheduled"))
	g.Expect(needsRebalancing).To(BeFalse(), "should not need rebalancing with unscheduled pods")
}

func TestCoreDNSNeedsRebalancing_AllPodsPending(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// Both pods pending (no NodeName)
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-1",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      "coredns-2",
				Labels:    map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
			},
			Spec: corev1.PodSpec{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(&pods[0], &pods[1]).
		Build()

	reconciler := &controller{
		logger: ctrl.Log.WithName("test"),
		client: fakeClient,
		snap:   nil,
	}

	needsRebalancing, err := reconciler.coreDNSNeedsRebalancing(ctx)

	g.Expect(err.Error()).To(Equal("less than 2 pods are scheduled"))
	g.Expect(needsRebalancing).To(BeFalse(), "should not need rebalancing when no pods scheduled")
}

func TestCoreDNSNeedsRebalancing_NoPods(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		Build()

	reconciler := &controller{
		logger: ctrl.Log.WithName("test"),
		client: fakeClient,
		snap:   nil,
	}

	needsRebalancing, err := reconciler.coreDNSNeedsRebalancing(ctx)

	g.Expect(err.Error()).To(Equal("no CoreDNS pods found"))
	g.Expect(needsRebalancing).To(BeFalse())
}

func TestReconcile_RestartsDeploymentForCoLocatedPods(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	reconciler, k8sClient := newReconcileTestController(t, nil)

	result, err := reconciler.Reconcile(ctx, ctrl.Request{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))
	g.Expect(deploymentUpdateCount(k8sClient)).To(Equal(1))

	restartedDeployment, err := k8sClient.AppsV1().Deployments(corednsNamespace).Get(ctx, corednsDeployment, metav1.GetOptions{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(restartedDeployment.Spec.Template.Annotations).To(HaveKey(restartedAtAnnotation))
}

func TestReconcile_SafetyGuards(t *testing.T) {
	for _, test := range []struct {
		name        string
		mutate      func(*corev1.Node, *corev1.Pod, *appsv1.Deployment)
		wantRequeue bool
	}{
		{"NodeNotReady", func(node *corev1.Node, _ *corev1.Pod, _ *appsv1.Deployment) {
			node.Status.Conditions[0].Status = corev1.ConditionFalse
		}, false},
		{"NodeCordoned", func(node *corev1.Node, _ *corev1.Pod, _ *appsv1.Deployment) {
			node.Spec.Unschedulable = true
		}, false},
		{"CNIStartupTaint", func(node *corev1.Node, _ *corev1.Pod, _ *appsv1.Deployment) {
			node.Spec.Taints = []corev1.Taint{{Key: "node.cilium.io/agent-not-ready", Effect: corev1.TaintEffectNoSchedule}}
		}, false},
		{"NoExecuteTaint", func(node *corev1.Node, _ *corev1.Pod, _ *appsv1.Deployment) {
			node.Spec.Taints = []corev1.Taint{{Key: "maintenance", Effect: corev1.TaintEffectNoExecute}}
		}, false},
		{"ControlPlaneNoExecuteTaint", func(node *corev1.Node, _ *corev1.Pod, _ *appsv1.Deployment) {
			node.Spec.Taints = []corev1.Taint{{Key: coredns.ControlPlaneTaintKey, Effect: corev1.TaintEffectNoExecute}}
		}, false},
		{"PodNotReady", func(_ *corev1.Node, pod *corev1.Pod, _ *appsv1.Deployment) {
			pod.Status.Conditions[0].Status = corev1.ConditionFalse
		}, true},
		{"YoungPod", func(_ *corev1.Node, pod *corev1.Pod, _ *appsv1.Deployment) {
			pod.CreationTimestamp = metav1.Now()
		}, true},
		{"RecentRestart", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Spec.Template.Annotations = map[string]string{restartedAtAnnotation: time.Now().Format(time.RFC3339)}
		}, true},
		{"UnavailableReplica", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Status.UnavailableReplicas = 1
		}, true},
		{"UnreadyReplica", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Status.ReadyReplicas = 1
		}, true},
		{"UnobservedGeneration", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Generation++
		}, true},
		{"OldReadyReplicas", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Status.UpdatedReplicas = 1
		}, true},
		{"MinReadySecondsPending", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Status.AvailableReplicas = 1
		}, true},
		{"ScaleUpPending", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			*deployment.Spec.Replicas = 3
		}, true},
		{"ScaleDownPending", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			*deployment.Spec.Replicas = 1
		}, true},
		{"ScaleToZeroPending", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			*deployment.Spec.Replicas = 0
		}, true},
		{"DefaultReplicaCount", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Spec.Replicas = nil
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			reconciler, k8sClient := newReconcileTestController(t, test.mutate)

			result, err := reconciler.Reconcile(context.Background(), ctrl.Request{})

			g.Expect(err).ToNot(HaveOccurred())
			wantResult := ctrl.Result{}
			if test.wantRequeue {
				wantResult = ctrl.Result{RequeueAfter: minRebalanceInterval}
			}
			g.Expect(result).To(Equal(wantResult))
			wantAttempts := 0
			if test.wantRequeue {
				wantAttempts = 1
			}
			g.Expect(reconciler.settleAttempts).To(Equal(wantAttempts))
			g.Expect(deploymentUpdateCount(k8sClient)).To(BeZero())
		})
	}
}

func TestReconcile_BackToBackNodeEvents(t *testing.T) {
	for _, observeRestart := range []bool{true, false} {
		name := "StaleDeploymentCache"
		if observeRestart {
			name = "ObservedRestart"
		}
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			reconciler, k8sClient := newReconcileTestController(t, nil)

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: "node-1"}})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(ctrl.Result{}))
			g.Expect(deploymentUpdateCount(k8sClient)).To(Equal(1))

			if observeRestart {
				updated, err := k8sClient.AppsV1().Deployments(corednsNamespace).Get(ctx, corednsDeployment, metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred())
				cached := &appsv1.Deployment{}
				g.Expect(reconciler.client.Get(ctx, client.ObjectKeyFromObject(updated), cached)).To(Succeed())
				cached.Spec = updated.Spec
				g.Expect(reconciler.client.Update(ctx, cached)).To(Succeed())
			}

			result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: "node-2"}})
			g.Expect(err).ToNot(HaveOccurred())
			// The cooldown / in-flight rollout guards skip the second rebalance
			// but requeue so it is retried once CoreDNS has settled.
			g.Expect(result).To(Equal(ctrl.Result{RequeueAfter: minRebalanceInterval}))
			g.Expect(reconciler.settleAttempts).To(Equal(1))
			g.Expect(deploymentUpdateCount(k8sClient)).To(Equal(1))
		})
	}
}

func TestReconcile_SettleBackoff(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	reconciler, k8sClient := newReconcileTestController(t, func(_ *corev1.Node, pod *corev1.Pod, _ *appsv1.Deployment) {
		pod.Status.Conditions[0].Status = corev1.ConditionFalse
	})

	// Consecutive skips double the requeue interval up to the cap.
	for i, want := range []time.Duration{
		minRebalanceInterval,
		2 * minRebalanceInterval,
		4 * minRebalanceInterval,
		8 * minRebalanceInterval,
	} {
		result, err := reconciler.Reconcile(ctx, ctrl.Request{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{RequeueAfter: want}), "attempt %d", i+1)
	}
	g.Expect(deploymentUpdateCount(k8sClient)).To(BeZero())

	// A successful rebalance resets the backoff.
	reconciler, k8sClient = newReconcileTestController(t, nil)
	reconciler.settleAttempts = 10
	result, err := reconciler.Reconcile(ctx, ctrl.Request{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))
	g.Expect(reconciler.settleAttempts).To(BeZero())
	g.Expect(deploymentUpdateCount(k8sClient)).To(Equal(1))
}

func TestSettleRequeueInterval(t *testing.T) {
	g := NewWithT(t)
	for _, test := range []struct {
		attempts int
		want     time.Duration
	}{
		{0, minRebalanceInterval},
		{1, minRebalanceInterval},
		{2, 2 * minRebalanceInterval},
		{3, 4 * minRebalanceInterval},
		{10, maxRebalanceInterval},
		{100, maxRebalanceInterval},
	} {
		reconciler := &controller{settleAttempts: test.attempts}
		g.Expect(reconciler.settleRequeueInterval()).To(Equal(test.want), "attempts=%d", test.attempts)
	}
}

func deploymentUpdateCount(k8sClient *k8sfake.Clientset) int {
	count := 0
	for _, action := range k8sClient.Actions() {
		if action.Matches("update", "deployments") || action.Matches("patch", "deployments") {
			count++
		}
	}
	return count
}

func TestReconcile_AllowedRebalances(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*corev1.Node, *corev1.Pod, *appsv1.Deployment)
	}{
		{"ControlPlaneTaint", func(node *corev1.Node, _ *corev1.Pod, _ *appsv1.Deployment) {
			node.Spec.Taints = []corev1.Taint{{Key: coredns.ControlPlaneTaintKey, Effect: corev1.TaintEffectNoSchedule}}
		}},
		{"PreferNoSchedule", func(node *corev1.Node, _ *corev1.Pod, _ *appsv1.Deployment) {
			node.Spec.Taints = []corev1.Taint{{Key: "preference", Effect: corev1.TaintEffectPreferNoSchedule}}
		}},
		{"ExpiredCooldown", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Spec.Template.Annotations = map[string]string{restartedAtAnnotation: time.Now().Add(-time.Minute).Format(time.RFC3339)}
		}},
		{"MalformedRestartAnnotation", func(_ *corev1.Node, _ *corev1.Pod, deployment *appsv1.Deployment) {
			deployment.Spec.Template.Annotations = map[string]string{restartedAtAnnotation: "invalid"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			reconciler, k8sClient := newReconcileTestController(t, test.mutate)

			result, err := reconciler.Reconcile(context.Background(), ctrl.Request{})

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(ctrl.Result{}))
			g.Expect(deploymentUpdateCount(k8sClient)).To(Equal(1))
		})
	}
}

func TestReconcile_ClusterConfig(t *testing.T) {
	configError := errors.New("cluster config unavailable")
	disabled := false
	for _, test := range []struct {
		name      string
		getConfig func(context.Context) (types.ClusterConfig, error)
		wantError error
	}{
		{name: "NilGetter"},
		{
			name: "DNSDisabled",
			getConfig: func(context.Context) (types.ClusterConfig, error) {
				return types.ClusterConfig{DNS: types.DNS{Enabled: &disabled}}, nil
			},
		},
		{
			name: "ConfigError",
			getConfig: func(context.Context) (types.ClusterConfig, error) {
				return types.ClusterConfig{}, configError
			},
			wantError: configError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			reconciler, k8sClient := newReconcileTestController(t, nil)
			reconciler.getClusterConfig = test.getConfig

			result, err := reconciler.Reconcile(context.Background(), ctrl.Request{})

			g.Expect(errors.Is(err, test.wantError)).To(BeTrue())
			g.Expect(result).To(Equal(ctrl.Result{}))
			g.Expect(reconciler.settleAttempts).To(BeZero(), "non-settle paths must not advance the backoff")
			g.Expect(k8sClient.Actions()).To(BeEmpty())
		})
	}
}

func TestReconcile_DeploymentErrors(t *testing.T) {
	for _, test := range []struct {
		name        string
		verb        string
		apiError    error
		wantError   bool
		wantUpdates int
	}{
		{
			name:     "DeploymentMissing",
			verb:     "get",
			apiError: apierrors.NewNotFound(appsv1.Resource("deployments"), corednsDeployment),
		},
		{
			name:      "ReadFailure",
			verb:      "get",
			apiError:  errors.New("API unavailable"),
			wantError: true,
		},
		{
			name:        "UpdateConflict",
			verb:        "update",
			apiError:    apierrors.NewConflict(appsv1.Resource("deployments"), corednsDeployment, errors.New("changed resource version")),
			wantError:   true,
			wantUpdates: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			reconciler, k8sClient := newReconcileTestController(t, nil)
			k8sClient.PrependReactor(test.verb, "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, test.apiError
			})

			result, err := reconciler.Reconcile(context.Background(), ctrl.Request{})

			if test.wantError {
				g.Expect(errors.Is(err, test.apiError)).To(BeTrue())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(ctrl.Result{}))
			g.Expect(deploymentUpdateCount(k8sClient)).To(Equal(test.wantUpdates))
		})
	}
}

func newReconcileTestController(t *testing.T, mutate func(*corev1.Node, *corev1.Pod, *appsv1.Deployment)) (*controller, *k8sfake.Clientset) {
	t.Helper()
	enabled := true
	replicas := int32(2)

	nodes := []corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		},
	}
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:         "kube-system",
				Name:              "coredns-1",
				Labels:            map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
				CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Minute)),
			},
			Spec:   corev1.PodSpec{NodeName: "node-1"},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:         "kube-system",
				Name:              "coredns-2",
				Labels:            map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"},
				CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Minute)),
			},
			Spec:   corev1.PodSpec{NodeName: "node-1"},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system", Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           2,
			UpdatedReplicas:    2,
			ReadyReplicas:      2,
			AvailableReplicas:  2,
		},
	}
	if mutate != nil {
		mutate(&nodes[1], &pods[0], deployment)
	}
	k8sClient := k8sfake.NewSimpleClientset(deployment.DeepCopy())

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(&nodes[0], &nodes[1], &pods[0], &pods[1], deployment).
		Build()

	reconciler := &controller{
		logger: ctrl.Log.WithName("test"),
		client: fakeClient,
		getClusterConfig: func(context.Context) (types.ClusterConfig, error) {
			return types.ClusterConfig{DNS: types.DNS{Enabled: &enabled}}, nil
		},
		snap: &snapmock.Snap{Mock: snapmock.Mock{KubernetesClient: &kubernetes.Client{Interface: k8sClient}}},
	}

	return reconciler, k8sClient
}
