package types

import (
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

// FeatureStatus encapsulates the deployment status of a feature.
type FeatureStatus struct {
	// Component shows the feature component name. For example, cilium for network feature.
	Component string
	// Enabled shows whether or not the deployment of manifests for a status was successful.
	Enabled bool
	// State indicates the current state of the component
	// TODO: enabled / disabled / failed can be determined in the ApplyNetwork function. But degraded / waiting should be computed at runtime, or other ways to be determined.
	State apiv2.FeatureState
	// Message contains information about the status of a feature. It is only supposed to be human readable and informative and should not be programmatically parsed.
	Message string
	// Version shows the version of the deployed feature.
	Version string
	// UpdatedAt shows when the last update was done.
	UpdatedAt time.Time
}

func (f FeatureStatus) ToAPI() apiv2.FeatureStatus {
	return apiv2.FeatureStatus{
		Enabled:   f.Enabled,
		Component: f.Component,
		State:     apiv2.FeatureStateEnabled,
		Message:   f.Message,
		Version:   f.Version,
		UpdatedAt: f.UpdatedAt,
	}
}

func FeatureStatusFromAPI(apiFS apiv2.FeatureStatus) FeatureStatus {
	return FeatureStatus{
		Enabled:   apiFS.Enabled,
		Component: apiFS.Component,
		State:     apiFS.State,
		Message:   apiFS.Message,
		Version:   apiFS.Version,
		UpdatedAt: apiFS.UpdatedAt,
	}
}
