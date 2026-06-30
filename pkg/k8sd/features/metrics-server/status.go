package metrics_server

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	probeUtil "github.com/canonical/k8sd/pkg/k8sd/features/podHealthProbe"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
)

// metricsServerNamespace is where the metrics-server workload is deployed.
const metricsServerNamespace = "kube-system"

// metrics-server workload identifiers used by CheckMetricsServer.
const (
	metricsServerWorkload   = "metrics-server"
	metricsServerLabelKey   = "app.kubernetes.io/name"
	metricsServerLabelValue = "metrics-server"
)

// CheckMetricsServer probes the metrics-server workload.
// Empty ProbeResult ⇒ healthy, no overlay.
func CheckMetricsServer(ctx context.Context, sn snap.Snap) types.ProbeResult {
	client, err := sn.KubernetesClient("")
	if err != nil {
		return metricsServerDegraded(err)
	}

	result := probeUtil.ProbeWorkload(ctx, client, metricsServerNamespace, metricsServerWorkload,
		map[string]string{metricsServerLabelKey: metricsServerLabelValue})
	if result.ProbeErr != nil {
		return metricsServerDegraded(result.ProbeErr)
	}

	return probeUtil.AggregateProbeResults(result)
}

// metricsServerDegraded wraps an error into the standard Degraded ProbeResult
// for the metrics-server probe.
func metricsServerDegraded(err error) types.ProbeResult {
	return types.ProbeResult{
		State:   apiv2.FeatureStateDegraded,
		Message: fmt.Sprintf("Could not verify metrics-server pod health: %v", err),
		Err:     err,
	}
}
