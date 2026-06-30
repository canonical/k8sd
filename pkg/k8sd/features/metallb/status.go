package metallb

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	probeUtil "github.com/canonical/k8sd/pkg/k8sd/features/podHealthProbe"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"golang.org/x/sync/errgroup"
)

// metallbNamespace is where all metallb-managed workloads are deployed.
const metallbNamespace = "metallb-system"

// LoadBalancer workload identifiers used by CheckLoadBalancer.
const (
	lbControllerWorkload       = "metallb-controller"
	lbSpeakerWorkload          = "metallb-speaker"
	lbNameLabelKey             = "app.kubernetes.io/name"
	lbNameLabelValue           = "metallb"
	lbComponentLabelKey        = "app.kubernetes.io/component"
	lbControllerComponentValue = "controller"
	lbSpeakerComponentValue    = "speaker"
)

// CheckLoadBalancer probes the metallb controller and speaker workloads.
// Empty ProbeResult ⇒ healthy, no overlay.
func CheckLoadBalancer(ctx context.Context, sn snap.Snap) types.ProbeResult {
	client, err := sn.KubernetesClient("")
	if err != nil {
		return loadBalancerDegraded(err)
	}

	var controller, speaker types.WorkloadResult
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		controller = probeUtil.ProbeWorkload(gctx, client, metallbNamespace, lbControllerWorkload,
			map[string]string{
				lbNameLabelKey:      lbNameLabelValue,
				lbComponentLabelKey: lbControllerComponentValue,
			})
		return nil
	})
	g.Go(func() error {
		speaker = probeUtil.ProbeWorkload(gctx, client, metallbNamespace, lbSpeakerWorkload,
			map[string]string{
				lbNameLabelKey:      lbNameLabelValue,
				lbComponentLabelKey: lbSpeakerComponentValue,
			})
		return nil
	})
	_ = g.Wait()

	// Speaker checked first so its message wins when both list calls fail.
	if speaker.ProbeErr != nil {
		return loadBalancerDegraded(speaker.ProbeErr)
	}
	if controller.ProbeErr != nil {
		return loadBalancerDegraded(controller.ProbeErr)
	}

	return probeUtil.AggregateProbeResults(controller, speaker)
}

// loadBalancerDegraded wraps an error into the standard Degraded ProbeResult
// for the load-balancer probe.
func loadBalancerDegraded(err error) types.ProbeResult {
	return types.ProbeResult{
		State:   apiv2.FeatureStateDegraded,
		Message: fmt.Sprintf("Could not verify metallb pod health: %v", err),
		Err:     err,
	}
}
