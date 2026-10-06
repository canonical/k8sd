package helm

// Patch describes a single Kustomize-style patch that the kustomizePostRenderer
// applies to a Helm-rendered manifest before it is installed/upgraded.
//
// Exactly one of StrategicMerge or JSON6902 must be set. Kustomize itself
// auto-detects which patch style was supplied based on its content shape (an
// object for strategic-merge, a list of operations for JSON6902), so callers
// only need to pick the correct field.
type Patch struct {
	Target         PatchTarget
	StrategicMerge string
	JSON6902       string
}

// PatchTarget identifies the resource a Patch applies to.
type PatchTarget struct {
	Kind      string
	Name      string
	Namespace string
}
