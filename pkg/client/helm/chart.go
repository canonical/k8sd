package helm

// InstallableChart describes a chart that can be deployed on a running cluster.
type InstallableChart struct {
	// Name is the install name of the chart.
	Name string

	// Namespace is the namespace to install the chart.
	Namespace string

	// ManifestPath is the path to the chart's manifest, typically relative to "$SNAP/k8s/manifests".
	// TODO(neoaggelos): this should be a *chart.Chart, and we should use the "embed" package to load it when building k8sd.
	ManifestPath string

	// FullOwnership indicates that k8sd is the sole owner of this chart's Helm values.
	// When true, Apply uses ResetValues=true so that any previously set keys not present
	// in the new values (e.g. removed ConfigMap override keys) are properly reverted.
	// Charts shared between multiple features (e.g. ChartCilium, managed by both
	// ApplyNetwork and ApplyGateway/ApplyIngress) must leave this false so that
	// partial-update callers don't wipe each other's values.
	FullOwnership bool
}
