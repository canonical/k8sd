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
	return probeCiliumWorkloads(ctx, sn, "network")
}

// CheckIngress probes cilium's health for the ingress feature. Cilium serves
// ingress via the Envoy proxy embedded in the cilium-agent (k8sd disables the
// standalone cilium-envoy DaemonSet), so ingress health derives from the same
// operator + agent workloads as the network feature.
func CheckIngress(ctx context.Context, sn snap.Snap) types.ProbeResult {
	return probeCiliumWorkloads(ctx, sn, "ingress")
}

// CheckGateway probes cilium's health for the gateway feature. Like ingress,
// the Gateway API is served by the cilium-operator + cilium-agent workloads,
// so gateway health derives from probing those.
func CheckGateway(ctx context.Context, sn snap.Snap) types.ProbeResult {
	return probeCiliumWorkloads(ctx, sn, "gateway")
}

// probeCiliumWorkloads probes the cilium operator and agent workloads and
// aggregates the result. feature is used only to qualify the Degraded message
// (e.g. "Could not verify cilium <feature> pod health").
// Empty ProbeResult ⇒ healthy, no overlay.
func probeCiliumWorkloads(ctx context.Context, sn snap.Snap, feature string) types.ProbeResult {
	client, err := sn.KubernetesClient("")
	if err != nil {
		return ciliumDegraded(feature, err)
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
		return ciliumDegraded(feature, agent.ProbeErr)
	}
	if operator.ProbeErr != nil {
		return ciliumDegraded(feature, operator.ProbeErr)
	}

	return probeUtil.AggregateProbeResults(operator, agent)
}

// ciliumDegraded wraps an error into the standard Degraded ProbeResult for a
// cilium feature probe.
func ciliumDegraded(feature string, err error) types.ProbeResult {
	return types.ProbeResult{
		State:   apiv2.FeatureStateDegraded,
		Message: fmt.Sprintf("Could not verify cilium %s pod health: %v", feature, err),
		Err:     err,
	}
}

// CheckLoadBalancer probes cilium's health for the load-balancer feature.
// Cilium's LoadBalancer (L2 announcements / BGP / externalIPs) is served by
// the cilium-operator + cilium-agent workloads rather than dedicated pods, so
// load-balancer health derives from probing those.
func CheckLoadBalancer(ctx context.Context, sn snap.Snap) types.ProbeResult {
	return probeCiliumWorkloads(ctx, sn, "load-balancer")
}
