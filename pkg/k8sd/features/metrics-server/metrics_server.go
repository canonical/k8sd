package metrics_server

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
)

const (
	enabledMsg          = "Collecting resource metrics from kubelets"
	disabledMsg         = "disabled"
	deleteFailedMsgTmpl = "Failed to delete Metrics Server, the error was: %v"
	deployFailedMsgTmpl = "Failed to deploy Metrics Server, the error was: %v"
	component           = "metrics-server"
)

// ApplyMetricsServer deploys metrics-server when cfg.Enabled is true.
// ApplyMetricsServer removes metrics-server when cfg.Enabled is false.
// ApplyMetricsServer will always return a FeatureStatus indicating the current status of the
// deployment.
// ApplyMetricsServer returns an error if anything fails. The error is also wrapped in the .Message field of the
// returned FeatureStatus.
func ApplyMetricsServer(ctx context.Context, snap snap.Snap, cfg types.MetricsServer, annotations types.Annotations) (types.FeatureStatus, error) {
	m := snap.HelmClient()

	config := internalConfig(annotations)

	values := map[string]any{
		"image": map[string]any{
			"repository": config.imageRepo,
			"tag":        config.imageTag,
		},
		"securityContext": map[string]any{
			// ROCKs with Pebble as the entrypoint do not work with this option.
			"readOnlyRootFilesystem": false,
		},
	}

	_, err := m.Apply(ctx, chart, helm.StatePresentOrDeleted(cfg.GetEnabled()), values)
	if err != nil {
		if cfg.GetEnabled() {
			err = fmt.Errorf("failed to install metrics server chart: %w", err)
			return types.FeatureStatus{
				Enabled:   false,
				State:     apiv2.FeatureStateFailed,
				Component: component,
				Version:   imageTag,
				Message:   fmt.Sprintf(deployFailedMsgTmpl, err),
			}, err
		} else {
			err = fmt.Errorf("failed to delete metrics server chart: %w", err)
			return types.FeatureStatus{
				Enabled:   false,
				State:     apiv2.FeatureStateFailed,
				Component: component,
				Version:   imageTag,
				Message:   fmt.Sprintf(deleteFailedMsgTmpl, err),
			}, err
		}
	} else {
		if cfg.GetEnabled() {
			return types.FeatureStatus{
				Enabled:   true,
				State:     apiv2.FeatureStateEnabled,
				Component: component,
				Version:   imageTag,
				Message:   enabledMsg,
			}, nil
		} else {
			return types.FeatureStatus{
				Enabled:   false,
				State:     apiv2.FeatureStateDisabled,
				Component: component,
				Version:   imageTag,
				Message:   disabledMsg,
			}, nil
		}
	}
}
