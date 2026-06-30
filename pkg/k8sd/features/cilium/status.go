package cilium

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	probeUtil "github.com/canonical/k8sd/pkg/k8sd/features/podHealthProbe"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"golang.org/x/sync/errgroup"
)

const (
	DisabledMsg = "disabled"
	EnabledMsg  = "enabled"
)

// ciliumNamespace is where all cilium-managed workloads are deployed.
const ciliumNamespace = "kube-system"

// Network-specific workload identifiers used by CheckNetwork.
const (
	networkOperatorWorkload   = "cilium-operator"
	networkOperatorLabelKey   = "io.cilium/app"
	networkOperatorLabelValue = "operator"

	networkAgentWorkload   = "cilium-agent"
	networkAgentLabelKey   = "k8s-app"
	networkAgentLabelValue = "cilium"
)

// CheckNetwork probes the cilium operator and agent workloads.
// Empty ProbeResult ⇒ healthy, no overlay.
func CheckNetwork(ctx context.Context, sn snap.Snap) types.ProbeResult {
	client, err := sn.KubernetesClient("")
	if err != nil {
		return networkDegraded(err)
	}

	var operator, agent types.WorkloadResult
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		operator = probeUtil.ProbeWorkload(gctx, client, ciliumNamespace, networkOperatorWorkload,
			map[string]string{networkOperatorLabelKey: networkOperatorLabelValue})
		return nil
	})
	g.Go(func() error {
		agent = probeUtil.ProbeWorkload(gctx, client, ciliumNamespace, networkAgentWorkload,
			map[string]string{networkAgentLabelKey: networkAgentLabelValue})
		return nil
	})
	_ = g.Wait()

	// Agent checked first so its message wins when both list calls fail.
	if agent.ProbeErr != nil {
		return networkDegraded(agent.ProbeErr)
	}
	if operator.ProbeErr != nil {
		return networkDegraded(operator.ProbeErr)
	}

	return probeUtil.AggregateProbeResults(operator, agent)
}

// networkDegraded wraps an error into the standard Degraded ProbeResult
// for the network probe.
func networkDegraded(err error) types.ProbeResult {
	return types.ProbeResult{
		State:   apiv2.FeatureStateDegraded,
		Message: fmt.Sprintf("Could not verify cilium network pod health: %v", err),
		Err:     err,
	}
}

// CheckIngress is a placeholder; a real probe will be added later.
func CheckIngress(ctx context.Context, sn snap.Snap) types.ProbeResult {
	return types.ProbeResult{}
}

// CheckGateway is a placeholder; a real probe will be added later.
func CheckGateway(ctx context.Context, sn snap.Snap) types.ProbeResult {
	return types.ProbeResult{}
}

// CheckLoadBalancer is a placeholder; a real probe will be added later.
func CheckLoadBalancer(ctx context.Context, sn snap.Snap) types.ProbeResult {
	return types.ProbeResult{}
}
