package dnsrebalancer

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	minRebalanceInterval  = 30 * time.Second
	restartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"
	controlPlaneTaintKey  = "node-role.kubernetes.io/control-plane"
	corednsNamespace      = "kube-system"
	corednsDeployment     = "coredns"
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
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Sufficient nodes ready, checking CoreDNS distribution", "readyCount", readyCount)

	// Check if rebalancing is needed
	needsRebalancing, err := r.coreDNSNeedsRebalancing(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check CoreDNS pods distribution: %w", err)
	}

	if !needsRebalancing {
		return ctrl.Result{}, nil
	}

	k8sClient, err := r.snap.KubernetesClient("")
	if err != nil {
		return ctrl.Result{}, err
	}
	deployment, err := k8sClient.AppsV1().Deployments(corednsNamespace).Get(ctx, corednsDeployment, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get CoreDNS deployment: %w", err)
	}
	desiredReplicas := int32(1)
	if deployment.Spec.Replicas != nil {
		desiredReplicas = *deployment.Spec.Replicas
	}
	if desiredReplicas < 2 {
		return ctrl.Result{}, nil
	}
	if deployment.Status.ObservedGeneration < deployment.Generation ||
		deployment.Status.UpdatedReplicas != desiredReplicas ||
		deployment.Status.Replicas != desiredReplicas ||
		deployment.Status.ReadyReplicas != desiredReplicas ||
		deployment.Status.AvailableReplicas != desiredReplicas ||
		deployment.Status.UnavailableReplicas > 0 {
		log.V(1).Info("CoreDNS deployment rollout already in progress, skipping rebalance")
		return ctrl.Result{}, nil
	}
	if restartedRecently(deployment) {
		log.V(1).Info("CoreDNS deployment was recently restarted, skipping rebalance")
		return ctrl.Result{}, nil
	}

	scheduled, err := r.scheduledCoreDNSPods(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, pod := range scheduled {
		if pod.DeletionTimestamp != nil {
			return ctrl.Result{}, nil
		}
		if !podReady(&pod) || (!pod.CreationTimestamp.IsZero() && time.Since(pod.CreationTimestamp.Time) < minRebalanceInterval) {
			log.V(1).Info("CoreDNS pods are not yet settled, skipping rebalance")
			return ctrl.Result{}, nil
		}
	}

	log.Info("CoreDNS pods need rebalancing, triggering deployment rollout restart")

	if err := k8sClient.RestartDeployment(ctx, corednsDeployment, corednsNamespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to restart CoreDNS deployment: %w", err)
	}

	log.Info("Successfully triggered CoreDNS deployment restart")
	return ctrl.Result{}, nil
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

func restartedRecently(deployment *appsv1.Deployment) bool {
	if deployment.Spec.Template.Annotations == nil {
		return false
	}
	restartedAt := deployment.Spec.Template.Annotations[restartedAtAnnotation]
	if restartedAt == "" {
		return false
	}
	restartedAtTime, err := time.Parse(time.RFC3339, restartedAt)
	if err != nil {
		return false
	}
	return time.Since(restartedAtTime) < minRebalanceInterval
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
