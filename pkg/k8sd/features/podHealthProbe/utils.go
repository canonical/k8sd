package podhealthprobe

import (
	"context"
	"fmt"
	"sort"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// failingWaitingReasons / failingTerminatedReasons mark container states
// that indicate a hard failure (vs. transient pending).
var (
	failingWaitingReasons = map[string]struct{}{
		"CrashLoopBackOff":           {},
		"ErrImagePull":               {},
		"ImagePullBackOff":           {},
		"CreateContainerConfigError": {},
		"RunContainerError":          {},
	}
	failingTerminatedReasons = map[string]struct{}{
		"Error":     {},
		"OOMKilled": {},
	}
)

// probeWorkload lists pods matching labels in namespace, classifies them,
// and returns the workload verdict. Agnostic of cilium specifics — reusable
// for any feature probe.
func ProbeWorkload(ctx context.Context, client *kubernetes.Client, namespace, workload string, labels map[string]string) types.WorkloadResult {
	pods, err := client.ListPods(ctx, namespace, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: labels}),
	})
	if err != nil {
		return types.WorkloadResult{Workload: workload, ProbeErr: err}
	}

	// Sort for deterministic "first failing reason" output.
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })

	var (
		total       = len(pods)
		readyN      int
		failingN    int
		maxRestarts int32
		reason      string
	)
	for _, p := range pods {
		if kubernetes.PodIsReady(p) {
			readyN++
			continue
		}
		if r, restarts, isFailing := classifyFailingPod(p); isFailing {
			failingN++
			if reason == "" {
				reason = r
			}
			if restarts > maxRestarts {
				maxRestarts = restarts
			}
		}
		// Otherwise implicitly "waiting" (total - readyN - failingN).
	}

	switch {
	case failingN > 0:
		return types.WorkloadResult{
			Workload: workload,
			State:    apiv2.FeatureStateFailed,
			Message:  fmt.Sprintf("%s on %s (%d/%d pods, %d restarts max)", reason, workload, failingN, total, maxRestarts),
		}
	case total == 0:
		return types.WorkloadResult{
			Workload: workload,
			State:    apiv2.FeatureStateWaiting,
			Message:  fmt.Sprintf("Waiting for %s pods to be scheduled. %s", workload, checkEventsHint(namespace)),
		}
	case readyN < total:
		msg := fmt.Sprintf("Waiting for %s pods to become ready (%d/%d pods ready)", workload, readyN, total)
		if readyN == 0 {
			msg += ". " + checkEventsHint(namespace)
		}
		return types.WorkloadResult{
			Workload: workload,
			State:    apiv2.FeatureStateWaiting,
			Message:  msg,
		}
	default: // readyN == total > 0
		return types.WorkloadResult{Workload: workload, State: apiv2.FeatureStateEnabled}
	}
}

// checkEventsHint is appended to Waiting messages when no pods are ready,
// pointing the user at cluster events for the actual reason (scheduling
// failures, image pulls, etc.).
func checkEventsHint(namespace string) string {
	return fmt.Sprintf("Check cluster events for details: k8s kubectl get events -n %s", namespace)
}

// classifyFailingPod scans init, regular, and ephemeral containers and
// returns the first failing reason found, max restart count, and whether
// the pod counts as failing.
func classifyFailingPod(p corev1.Pod) (reason string, maxRestarts int32, isFailing bool) {
	scan := func(statuses []corev1.ContainerStatus) {
		for _, cs := range statuses {
			if cs.RestartCount > maxRestarts {
				maxRestarts = cs.RestartCount
			}
			if cs.State.Waiting != nil {
				if _, ok := failingWaitingReasons[cs.State.Waiting.Reason]; ok {
					isFailing = true
					if reason == "" {
						reason = cs.State.Waiting.Reason
					}
				}
			}
			if cs.State.Terminated != nil {
				if _, ok := failingTerminatedReasons[cs.State.Terminated.Reason]; ok {
					isFailing = true
					if reason == "" {
						reason = cs.State.Terminated.Reason
					}
				}
			}
		}
	}
	scan(p.Status.InitContainerStatuses)
	scan(p.Status.ContainerStatuses)
	scan(p.Status.EphemeralContainerStatuses)
	return reason, maxRestarts, isFailing
}

// aggregateProbeResults picks the highest-severity result (Failed >
// Degraded > Waiting > Enabled). Later args win ties. All-Enabled returns
// empty (no overlay).
func AggregateProbeResults(results ...types.WorkloadResult) types.ProbeResult {
	if len(results) == 0 {
		return types.ProbeResult{}
	}

	severity := func(s apiv2.FeatureState) int {
		switch s {
		case apiv2.FeatureStateFailed:
			return 3
		case apiv2.FeatureStateDegraded:
			return 2
		case apiv2.FeatureStateWaiting:
			return 1
		default:
			return 0
		}
	}

	bestIdx, bestSev := 0, -1
	for i, r := range results {
		// `>=` so later ties win.
		if sev := severity(r.State); sev >= bestSev {
			bestIdx, bestSev = i, sev
		}
	}

	if bestSev == 0 {
		return types.ProbeResult{}
	}
	return types.ProbeResult{
		State:   results[bestIdx].State,
		Message: results[bestIdx].Message,
	}
}
