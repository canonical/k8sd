package localpv

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	probeUtil "github.com/canonical/k8sd/pkg/k8sd/features/podHealthProbe"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"golang.org/x/sync/errgroup"
)

// localpvNamespace is where the rawfile-csi workloads are deployed.
const localpvNamespace = "kube-system"

// LocalStorage workload identifiers used by CheckLocalStorage.
const (
	storageControllerWorkload = "rawfile-csi-controller"
	storageNodeWorkload       = "rawfile-csi-node"
	storageNameLabelKey       = "app.kubernetes.io/name"
	storageNameLabelValue     = "rawfile-csi"
	storageComponentLabelKey  = "component"
	storageControllerValue    = "controller"
	storageNodeValue          = "node"
)

// CheckLocalStorage probes the rawfile-csi controller and node workloads.
// Empty ProbeResult ⇒ healthy, no overlay.
func CheckLocalStorage(ctx context.Context, sn snap.Snap) types.ProbeResult {
	client, err := sn.KubernetesClient("")
	if err != nil {
		return localStorageDegraded(err)
	}

	var controller, node types.WorkloadResult
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		controller = probeUtil.ProbeWorkload(gctx, client, localpvNamespace, storageControllerWorkload,
			map[string]string{
				storageNameLabelKey:      storageNameLabelValue,
				storageComponentLabelKey: storageControllerValue,
			})
		return nil
	})
	g.Go(func() error {
		node = probeUtil.ProbeWorkload(gctx, client, localpvNamespace, storageNodeWorkload,
			map[string]string{
				storageNameLabelKey:      storageNameLabelValue,
				storageComponentLabelKey: storageNodeValue,
			})
		return nil
	})
	_ = g.Wait()

	// Node checked first so its message wins when both list calls fail.
	if node.ProbeErr != nil {
		return localStorageDegraded(node.ProbeErr)
	}
	if controller.ProbeErr != nil {
		return localStorageDegraded(controller.ProbeErr)
	}

	return probeUtil.AggregateProbeResults(controller, node)
}

// localStorageDegraded wraps an error into the standard Degraded ProbeResult
// for the local-storage probe.
func localStorageDegraded(err error) types.ProbeResult {
	return types.ProbeResult{
		State:   apiv2.FeatureStateDegraded,
		Message: fmt.Sprintf("Could not verify local-storage pod health: %v", err),
		Err:     err,
	}
}
