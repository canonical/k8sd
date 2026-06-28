package coredns

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CheckDNS checks the CoreDNS deployment in the cluster.
func CheckDNS(ctx context.Context, snap snap.Snap) types.ProbeResult {
	client, err := snap.KubernetesClient("kube-system")
	if err != nil {
		return types.ProbeResult{
			State:   apiv2.FeatureStateDegraded,
			Message: fmt.Sprintf("Could not verify dns pod health: %v", err),
			Err:     err,
		}
	}

	for _, check := range []struct {
		name      string
		namespace string
		labels    map[string]string
	}{
		{name: "coredns", namespace: "kube-system", labels: map[string]string{"app.kubernetes.io/name": "coredns"}},
	} {
		if err := client.CheckForReadyPods(ctx, check.namespace, metav1.ListOptions{
			LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: check.labels}),
		}); err != nil {
			return types.ProbeResult{
				State:   apiv2.FeatureStateDegraded,
				Message: fmt.Sprintf("Could not verify dns pod health: %v", err),
				Err:     err,
			}
		}
	}

	return types.ProbeResult{}
}
