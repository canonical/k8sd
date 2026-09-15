package helm

import (
	"bytes"
	"fmt"

	"helm.sh/helm/v3/pkg/postrender"
	"sigs.k8s.io/kustomize/api/krusty"
	ktypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/resid"
	"sigs.k8s.io/yaml"
)

const (
	kustomizeRoot         = "/"
	kustomizeBaseManifest = "/base.yaml"
	kustomizationFileName = "/kustomization.yaml"
)

// kustomizePostRenderer is a Helm postrender.PostRenderer that applies a set
// of Kustomize-style patches to the manifests rendered by Helm, entirely
// in-memory: no kustomize CLI binary and no files written to disk.
//
// NOTE: Helm only passes *regular* manifests through the post-renderer.
// Resources managed by a Helm lifecycle hook (e.g. "helm.sh/hook: pre-install")
// are never included in renderedManifests, and therefore can never be
// targeted by a Patch. Callers should surface this limitation to users if a
// patch target happens to be hook-managed.
type kustomizePostRenderer struct {
	patches          []Patch
	defaultNamespace string
}

// NewKustomizePostRenderer validates patches and returns a postrender.PostRenderer
// that applies them via Kustomize. Returns (nil, nil) when patches is empty,
// since no post-rendering is needed in that case (Helm treats a nil
// PostRenderer as a no-op).
func NewKustomizePostRenderer(patches []Patch, defaultNamespace string) (postrender.PostRenderer, error) {
	if len(patches) == 0 {
		return nil, nil
	}
	for i, p := range patches {
		if p.Target.Kind == "" || p.Target.Name == "" {
			return nil, fmt.Errorf("patches[%d]: target.kind and target.name are required", i)
		}
		if p.StrategicMerge == "" && p.JSON6902 == "" {
			return nil, fmt.Errorf("patches[%d]: exactly one of strategic-merge or json6902 must be set", i)
		}
		if p.StrategicMerge != "" && p.JSON6902 != "" {
			return nil, fmt.Errorf("patches[%d]: strategic-merge and json6902 are mutually exclusive", i)
		}
	}
	return &kustomizePostRenderer{patches: patches, defaultNamespace: defaultNamespace}, nil
}

// Run implements postrender.PostRenderer. It builds a synthetic, in-memory
// kustomization that treats the Helm-rendered manifest as its sole resource,
// applies the configured patches on top of it (letting Kustomize
// auto-detect strategic-merge vs. JSON6902 patch style from the content
// shape), and returns the patched manifest.
func (r *kustomizePostRenderer) Run(renderedManifests *bytes.Buffer) (*bytes.Buffer, error) {
	if err := r.validateTargetsExist(renderedManifests.Bytes()); err != nil {
		return nil, err
	}

	fSys := filesys.MakeFsInMemory()

	if err := fSys.WriteFile(kustomizeBaseManifest, renderedManifests.Bytes()); err != nil {
		return nil, fmt.Errorf("failed to write rendered manifest to in-memory filesystem: %w", err)
	}

	kustomization := ktypes.Kustomization{
		TypeMeta: ktypes.TypeMeta{
			APIVersion: ktypes.KustomizationVersion,
			Kind:       ktypes.KustomizationKind,
		},
		Resources: []string{kustomizeBaseManifest},
	}

	for _, p := range r.patches {
		namespace := p.Target.Namespace
		if namespace == "" {
			namespace = r.defaultNamespace
		}

		content := p.StrategicMerge
		if content == "" {
			content = p.JSON6902
		}

		kustomization.Patches = append(kustomization.Patches, ktypes.Patch{
			Patch: content,
			Target: &ktypes.Selector{
				ResId: resid.ResId{
					Gvk:       resid.Gvk{Kind: p.Target.Kind},
					Name:      p.Target.Name,
					Namespace: namespace,
				},
			},
		})
	}

	kustomizationYAML, err := yaml.Marshal(kustomization)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal generated kustomization: %w", err)
	}
	if err := fSys.WriteFile(kustomizationFileName, kustomizationYAML); err != nil {
		return nil, fmt.Errorf("failed to write kustomization to in-memory filesystem: %w", err)
	}

	rm, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(fSys, kustomizeRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to apply patches: %w", err)
	}

	out, err := rm.AsYaml()
	if err != nil {
		return nil, fmt.Errorf("failed to render patched manifest: %w", err)
	}

	return bytes.NewBuffer(out), nil
}

var _ postrender.PostRenderer = &kustomizePostRenderer{}

// validateTargetsExist gives a clear, k8sd-branded error when a patch target does not
// exist in the rendered manifest, rather than letting Kustomize silently no-op (which
// is what happens today for a JSON6902 patch whose target selector matches nothing).
func (r *kustomizePostRenderer) validateTargetsExist(manifest []byte) error {
	reader := kio.ByteReader{Reader: bytes.NewReader(manifest)}
	nodes, err := reader.Read()
	if err != nil {
		return fmt.Errorf("failed to parse rendered manifest: %w", err)
	}

	for i, p := range r.patches {
		namespace := p.Target.Namespace
		if namespace == "" {
			namespace = r.defaultNamespace
		}

		found := false
		for _, n := range nodes {
			if n.GetKind() != p.Target.Kind || n.GetName() != p.Target.Name {
				continue
			}
			// cluster-scoped resources have no namespace; namespaced resources
			// default to the release namespace if unset in the rendered manifest.
			resourceNamespace := n.GetNamespace()
			if resourceNamespace == "" || resourceNamespace == namespace {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf(
				"patches[%d]: no resource matching kind %q, name %q, namespace %q was found in the rendered manifest "+
					"(the target may be misspelled, or it may be a resource managed by a Helm lifecycle hook, which cannot be patched)",
				i, p.Target.Kind, p.Target.Name, namespace,
			)
		}
	}
	return nil
}
