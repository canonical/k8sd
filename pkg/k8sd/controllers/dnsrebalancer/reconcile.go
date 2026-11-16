package dnsrebalancer

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/features/coredns"
	"github.com/canonical/k8sd/pkg/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	minRebalanceInterval = 30 * time.Second
	// maxRebalanceInterval caps the exponential backoff applied to requeues
	// when CoreDNS keeps not being settled (30s, 60s, 120s, ..., 16m).
	maxRebalanceInterval  = 16 * time.Minute
	restartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"
	corednsNamespace      = "kube-system"
	corednsDeployment     = "coredns"
)

// corednsPodLabels selects the CoreDNS pods managed by the ck-dns chart.
var corednsPodLabels = map[string]string{"k8s-app": "coredns", "app.kubernetes.io/instance": "ck-dns"}

// Reconcile implements the reconciliation loop.
func (r *controller) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, reconcileErr error) {
	log := log.FromContext(ctx).WithValues("node", req.Name)
	defer func() {
		if reconcileErr != nil || result.RequeueAfter == 0 {
			r.settleAttempts = 0
		}
	}()

	requeueAfterSettle := func() (ctrl.Result, error) {
		r.settleAttempts++
		return ctrl.Result{RequeueAfter: r.settleRequeueInterval()}, nil
	}

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
	schedulableCount := 0
	nodesUnchanged := r.rebalancedNodes != nil
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if nodeSchedulableForCoreDNS(node) {
			schedulableCount++
			if _, ok := r.rebalancedNodes[node.Name]; !ok {
				nodesUnchanged = false
			}
		}
	}
	// Our own rollout's pod events must not trigger another restart.
	if nodesUnchanged && schedulableCount == len(r.rebalancedNodes) {
		return ctrl.Result{}, nil
	}
	r.rebalancedNodes = nil
	if schedulableCount < 2 {
		log.V(1).Info("Less than 2 nodes are schedulable for CoreDNS, skipping rebalancing",
			"readyCount", readyCount, "schedulableCount", schedulableCount)
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
		log.V(1).Info("CoreDNS deployment has less than 2 replicas, skipping rebalance", "desiredReplicas", desiredReplicas)
		return requeueAfterSettle()
	}
	if deployment.Status.ObservedGeneration < deployment.Generation ||
		deployment.Status.UpdatedReplicas != desiredReplicas ||
		deployment.Status.Replicas != desiredReplicas ||
		deployment.Status.ReadyReplicas != desiredReplicas ||
		deployment.Status.AvailableReplicas != desiredReplicas ||
		deployment.Status.UnavailableReplicas > 0 {
		log.V(1).Info("CoreDNS deployment rollout already in progress, skipping rebalance")
		return requeueAfterSettle()
	}
	if restartedRecently(deployment) {
		log.V(1).Info("CoreDNS deployment was recently restarted, skipping rebalance")
		return requeueAfterSettle()
	}

	pods := &corev1.PodList{}
	if err := r.client.List(ctx, pods, client.InNamespace(corednsNamespace), client.MatchingLabels(corednsPodLabels)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list CoreDNS pods: %w", err)
	}
	if len(pods.Items) != int(desiredReplicas) {
		return requeueAfterSettle()
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil || pod.Spec.NodeName == "" || !podReady(pod) ||
			(!pod.CreationTimestamp.IsZero() && time.Since(pod.CreationTimestamp.Time) < minRebalanceInterval) {
			log.V(1).Info("CoreDNS pods are not yet settled, skipping rebalance")
			return requeueAfterSettle()
		}
	}
	if !coreDNSNeedsRebalancing(pods.Items) {
		return ctrl.Result{}, nil
	}

	log.Info("CoreDNS pods need rebalancing, triggering deployment rollout restart")

	if err := k8sClient.RestartDeployment(ctx, corednsDeployment, corednsNamespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to restart CoreDNS deployment: %w", err)
	}

	r.rebalancedNodes = make(map[string]struct{}, schedulableCount)
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if nodeSchedulableForCoreDNS(node) {
			r.rebalancedNodes[node.Name] = struct{}{}
		}
	}
	log.Info("Successfully triggered CoreDNS deployment restart")
	return ctrl.Result{}, nil
}

// settleRequeueInterval returns the backoff interval for the current
// consecutive settle-skip count: minRebalanceInterval doubled per attempt,
// capped at maxRebalanceInterval.
func (r *controller) settleRequeueInterval() time.Duration {
	interval := minRebalanceInterval
	for i := 1; i < r.settleAttempts && interval < maxRebalanceInterval; i++ {
		interval *= 2
	}
	return min(interval, maxRebalanceInterval)
}

// coreDNSNeedsRebalancing checks whether all settled pods are on the same node.
func coreDNSNeedsRebalancing(pods []corev1.Pod) bool {
	if len(pods) < 2 {
		return false
	}
	firstNodeName := pods[0].Spec.NodeName
	for i := 1; i < len(pods); i++ {
		if pods[i].Spec.NodeName != firstNodeName {
			return false
		}
	}
	return true
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

func nodeSchedulableForCoreDNS(node *corev1.Node) bool {
	if !isNodeReady(node) || node.Spec.Unschedulable {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		// CoreDNS tolerates the control-plane NoSchedule taint, see
		// pkg/k8sd/features/coredns/coredns.go.
		if taint.Key == coredns.ControlPlaneTaintKey && taint.Effect == corev1.TaintEffectNoSchedule {
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
