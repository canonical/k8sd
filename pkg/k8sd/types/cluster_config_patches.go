package types

// Patch describes a single Kustomize-style patch that is applied, in-process, to a
// rendered Helm manifest before it is installed/upgraded on the cluster.
//
// Exactly one of StrategicMerge or JSON6902 must be set.
//
// Patches are intentionally scoped to regular (non-hook) manifests. Resources
// managed by a Helm lifecycle hook (e.g. "helm.sh/hook: pre-install") are never
// passed through the patch pipeline and cannot be targeted.
type Patch struct {
	// Target identifies which rendered resource the patch applies to.
	Target PatchTarget
	// StrategicMerge is a partial YAML/JSON manifest that is strategically merged
	// into the target resource (equivalent to `kubectl patch --type=strategic`).
	// Mutually exclusive with JSON6902.
	StrategicMerge *string
	// JSON6902 is an RFC 6902 JSON Patch document (a YAML/JSON list of
	// add/remove/replace/... operations) applied to the target resource.
	// Mutually exclusive with StrategicMerge.
	JSON6902 *string
}

// PatchTarget identifies the resource a Patch applies to.
type PatchTarget struct {
	// Kind is the Kubernetes "kind" of the target resource, e.g. "DaemonSet".
	Kind string
	// Name is the metadata.name of the target resource.
	Name string
	// Namespace is the metadata.namespace of the target resource.
	// If empty, defaults to the namespace the feature's chart is installed into.
	Namespace string
}
