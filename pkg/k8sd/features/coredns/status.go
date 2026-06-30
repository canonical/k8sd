package coredns

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	probeUtil "github.com/canonical/k8sd/pkg/k8sd/features/podHealthProbe"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
)

// corednsNamespace is where the coredns workload is deployed.
const corednsNamespace = "kube-system"

// DNS-specific workload identifiers used by CheckDNS.
const (
	dnsWorkload   = "coredns"
	dnsLabelKey   = "app.kubernetes.io/name"
	dnsLabelValue = "coredns"
)

// CheckDNS probes the coredns workload.
// Empty ProbeResult ⇒ healthy, no overlay.
func CheckDNS(ctx context.Context, sn snap.Snap) types.ProbeResult {
	client, err := sn.KubernetesClient("")
	if err != nil {
		return dnsDegraded(err)
	}

	result := probeUtil.ProbeWorkload(ctx, client, corednsNamespace, dnsWorkload,
		map[string]string{dnsLabelKey: dnsLabelValue})
	if result.ProbeErr != nil {
		return dnsDegraded(result.ProbeErr)
	}

	return probeUtil.AggregateProbeResults(result)
}

// dnsDegraded wraps an error into the standard Degraded ProbeResult
// for the dns probe.
func dnsDegraded(err error) types.ProbeResult {
	return types.ProbeResult{
		State:   apiv2.FeatureStateDegraded,
		Message: fmt.Sprintf("Could not verify dns pod health: %v", err),
		Err:     err,
	}
}
