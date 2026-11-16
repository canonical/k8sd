package dnsrebalancer

import (
	"context"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type controller struct {
	logger           logr.Logger
	client           client.Client
	getClusterConfig func(context.Context) (types.ClusterConfig, error)
	snap             snap.Snap

	// settleAttempts counts consecutive settle skips.
	settleAttempts int
	// rebalancedNodes limits restarts to one per observed schedulable node set.
	rebalancedNodes map[string]struct{}
}

func NewController(
	logger logr.Logger,
	client client.Client,
	getClusterConfig func(context.Context) (types.ClusterConfig, error),
	snap snap.Snap,
) *controller {
	return &controller{
		logger:           logger,
		client:           client,
		getClusterConfig: getClusterConfig,
		snap:             snap,
	}
}

// SetupWithManager sets up the controller with the manager.
func (r *controller) SetupWithManager(mgr ctrl.Manager) error {
	// Node changes allow rebalancing; pod events wake checks waiting for settling.
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(nodeEventPredicate())).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(coreDNSPodEventPredicate())).
		Complete(r)
}

func nodeEventPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			node, ok := e.Object.(*corev1.Node)
			return ok && isNodeReady(node)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, ok := e.ObjectOld.(*corev1.Node)
			if !ok {
				return false
			}
			newNode, ok := e.ObjectNew.(*corev1.Node)
			if !ok {
				return false
			}
			if isNodeReady(oldNode) != isNodeReady(newNode) {
				return true
			}
			if oldNode.Spec.Unschedulable != newNode.Spec.Unschedulable {
				return true
			}
			return !taintsEqual(oldNode.Spec.Taints, newNode.Spec.Taints)
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// coreDNSPodEventPredicate ignores pod updates unrelated to scheduling or readiness.
func coreDNSPodEventPredicate() predicate.Funcs {
	selector := labels.SelectorFromSet(corednsPodLabels)
	isCoreDNSPod := func(obj client.Object) bool {
		pod, ok := obj.(*corev1.Pod)
		return ok && pod.Namespace == corednsNamespace && selector.Matches(labels.Set(pod.Labels))
	}
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return isCoreDNSPod(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPod, ok := e.ObjectOld.(*corev1.Pod)
			if !ok || !isCoreDNSPod(oldPod) {
				return false
			}
			newPod, ok := e.ObjectNew.(*corev1.Pod)
			if !ok {
				return false
			}
			return oldPod.Spec.NodeName != newPod.Spec.NodeName ||
				podReady(oldPod) != podReady(newPod) ||
				(oldPod.DeletionTimestamp == nil) != (newPod.DeletionTimestamp == nil)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return isCoreDNSPod(e.Object)
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func isNodeReady(node *corev1.Node) bool {
	if node == nil {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func taintsEqual(a, b []corev1.Taint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Value != b[i].Value || a[i].Effect != b[i].Effect {
			return false
		}
	}
	return true
}
