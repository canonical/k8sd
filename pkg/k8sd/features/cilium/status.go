package cilium

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DisabledMsg = "disabled"
	EnabledMsg  = "enabled"
)

func CheckNetwork(ctx context.Context, snap snap.Snap) types.ProbeResult {
	client, err := snap.KubernetesClient("kube-system")
	if err != nil {
		return types.ProbeResult{
			State:   apiv2.FeatureStateFailed,
			Message: fmt.Sprintf("Could not verify cilium pod health: %v", err),
			Err:     err,
		}
	}

	for _, check := range []struct {
		name      string
		namespace string
		labels    map[string]string
	}{
		{name: "cilium-operator", namespace: "kube-system", labels: map[string]string{"io.cilium/app": "operator"}},
		{name: "cilium", namespace: "kube-system", labels: map[string]string{"k8s-app": "cilium"}},
	} {
		if err := client.CheckForReadyPods(ctx, check.namespace, metav1.ListOptions{
			LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: check.labels}),
		}); err != nil {
			return types.ProbeResult{
				State:   apiv2.FeatureStateFailed,
				Message: fmt.Sprintf("Could not verify cilium pod health: %v", err),
				Err:     err,
			}
		}
	}

	return types.ProbeResult{}
}

func CheckIngress(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return types.ProbeResult{}
}

func CheckGateway(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return types.ProbeResult{}
}

func CheckLoadBalancer(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return types.ProbeResult{}
}
