package dnsrebalancer

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	requeueInterval      = 30 * time.Second
	minPodAge            = 30 * time.Second
	controlPlaneTaintKey = "node-role.kubernetes.io/control-plane"
	corednsNamespace     = "kube-system"
	corednsDeployment    = "coredns"
)

// Reconcile implements the reconciliation loop.
func (r *controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("node", req.Name)

	if r.getClusterConfig == nil {
		log.Info("getClusterConfig is nil")
		return ctrl.Result{}, nil
	}

	// skip reconcile if dns is disabled
	config, err := r.getClusterConfig(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster config: %w", err)
	}
	if !config.DNS.GetEnabled() {
		return ctrl.Result{}, nil
	}

	// Count ready nodes
	nodeList := &corev1.NodeList{}
	if err := r.client.List(ctx, nodeList); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	readyCount := countReadyNodes(nodeList)
	schedulableCount := countSchedulableNodes(nodeList)
	if schedulableCount < 2 {
		log.V(1).Info("Less than 2 nodes are schedulable for CoreDNS, skipping rebalancing",
			"readyCount", readyCount, "schedulableCount", schedulableCount)
		// Two Ready nodes can still be unschedulable (e.g. Cilium
		// node.cilium.io/agent-not-ready). Requeue so we retry when the taint
		// is cleared even if that Node update is missed.
		if readyCount >= 2 {
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Sufficient nodes ready, checking CoreDNS distribution", "readyCount", readyCount)

	// Check if rebalancing is needed
	needsRebalancing, err := r.coreDNSNeedsRebalancing(ctx)
	if err != nil {
		return ctrl.Result{RequeueAfter: requeueInterval}, fmt.Errorf("failed to check CoreDNS pods distribution: %w", err)
	}

	if !needsRebalancing {
		return ctrl.Result{}, nil
	}

	deployment := &appsv1.Deployment{}
	if err := r.client.Get(ctx, client.ObjectKey{Namespace: corednsNamespace, Name: corednsDeployment}, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get CoreDNS deployment: %w", err)
	}
	if deployment.Status.UnavailableReplicas > 0 || deployment.Status.ReadyReplicas != deployment.Status.Replicas {
		log.V(1).Info("CoreDNS deployment rollout already in progress, skipping rebalance")
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	scheduled, err := r.scheduledCoreDNSPods(ctx)
	if err != nil {
		return ctrl.Result{RequeueAfter: requeueInterval}, err
	}
	for _, pod := range scheduled {
		if pod.DeletionTimestamp != nil {
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
		if !podReady(&pod) || (!pod.CreationTimestamp.IsZero() && time.Since(pod.CreationTimestamp.Time) < minPodAge) {
			log.V(1).Info("CoreDNS pods are not yet settled, skipping rebalance")
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
	}

	// Delete a single co-located pod so the ReplicaSet recreates it while the
	// remaining replica is still visible to the scheduler. A full Deployment
	// restart creates both new pods at once and re-triggers the same-hash
	// concurrent-bind race.
	pod := scheduled[len(scheduled)-1]
	log.Info("CoreDNS pods need rebalancing, deleting one co-located pod", "pod", pod.Name, "node", pod.Spec.NodeName)
	if err := r.client.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to delete CoreDNS pod %s: %w", pod.Name, err)
	}

	log.Info("Successfully deleted co-located CoreDNS pod", "pod", pod.Name)
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

func (r *controller) scheduledCoreDNSPods(ctx context.Context) ([]corev1.Pod, error) {
	pods := &corev1.PodList{}
	if err := r.client.List(ctx, pods, client.InNamespace(corednsNamespace), client.MatchingLabels{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"}); err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no CoreDNS pods found")
	}

	scheduled := make([]corev1.Pod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		if pod.Spec.NodeName != "" {
			scheduled = append(scheduled, pod)
		}
	}
	if len(scheduled) < 2 {
		return nil, fmt.Errorf("less than 2 pods are scheduled")
	}
	return scheduled, nil
}

// coreDNSNeedsRebalancing checks if CoreDNS pods need rebalancing.
func (r *controller) coreDNSNeedsRebalancing(ctx context.Context) (bool, error) {
	scheduled, err := r.scheduledCoreDNSPods(ctx)
	if err != nil {
		return false, err
	}

	firstNodeName := scheduled[0].Spec.NodeName
	for _, pod := range scheduled[1:] {
		if pod.Spec.NodeName != firstNodeName {
			return false, nil
		}
	}
	return true, nil
}

// countReadyNodes counts the number of nodes with Ready condition.
func countReadyNodes(nodeList *corev1.NodeList) int {
	readyCount := 0
	for _, node := range nodeList.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				readyCount++
				break
			}
		}
	}
	return readyCount
}


func countSchedulableNodes(nodeList *corev1.NodeList) int {
	count := 0
	for i := range nodeList.Items {
		if nodeSchedulableForCoreDNS(&nodeList.Items[i]) {
			count++
		}
	}
	return count
}

func nodeSchedulableForCoreDNS(node *corev1.Node) bool {
	if !isNodeReady(node) || node.Spec.Unschedulable {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		if taint.Key == controlPlaneTaintKey && taint.Effect == corev1.TaintEffectNoSchedule {
			continue
		}
		return false
	}
	return true
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}