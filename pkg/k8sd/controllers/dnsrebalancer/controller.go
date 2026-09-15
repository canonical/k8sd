package dnsrebalancer

import (
	"context"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type controller struct {
	logger           logr.Logger
	client           client.Client
	getClusterConfig func(context.Context) (types.ClusterConfig, error)
	snap             snap.Snap
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
	// Watch Node resources. Do not reconcile on every update of an already-Ready
	// node (kubelet heartbeats rewrite Node status and previously caused a
	// restart storm). Reconcile when schedulability changes: Ready condition,
	// unschedulable flag, taints, or node deletion (downscale).
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(nodeEventPredicate()).
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
