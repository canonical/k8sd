package helm_test

import (
	"bytes"
	"testing"

	"github.com/canonical/k8sd/pkg/client/helm"
	. "github.com/onsi/gomega"
)

const testManifest = `apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: cilium
  namespace: kube-system
spec:
  template:
    spec:
      containers:
      - name: cilium-agent
        image: cilium/cilium:1.0
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cilium-config
  namespace: kube-system
data:
  foo: bar
`

func TestKustomizePostRenderer_NoPatches(t *testing.T) {
	g := NewWithT(t)

	pr, err := helm.NewKustomizePostRenderer(nil, "kube-system")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pr).To(BeNil(), "no post-renderer should be created when there are no patches")
}

func TestKustomizePostRenderer_StrategicMerge(t *testing.T) {
	g := NewWithT(t)

	patches := []helm.Patch{
		{
			Target: helm.PatchTarget{Kind: "DaemonSet", Name: "cilium"},
			StrategicMerge: `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: cilium
spec:
  template:
    spec:
      nodeSelector:
        custom-node-label: "true"
`,
		},
	}

	pr, err := helm.NewKustomizePostRenderer(patches, "kube-system")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pr).NotTo(BeNil())

	out, err := pr.Run(bytes.NewBufferString(testManifest))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(out.String()).To(ContainSubstring("custom-node-label"))
	// the unrelated ConfigMap must be preserved unchanged
	g.Expect(out.String()).To(ContainSubstring("cilium-config"))
}

func TestKustomizePostRenderer_JSON6902(t *testing.T) {
	g := NewWithT(t)

	patches := []helm.Patch{
		{
			Target: helm.PatchTarget{Kind: "ConfigMap", Name: "cilium-config"},
			JSON6902: `
- op: replace
  path: /data/foo
  value: patched
`,
		},
	}

	pr, err := helm.NewKustomizePostRenderer(patches, "kube-system")
	g.Expect(err).NotTo(HaveOccurred())

	out, err := pr.Run(bytes.NewBufferString(testManifest))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(out.String()).To(ContainSubstring("foo: patched"))
}

func TestKustomizePostRenderer_UnknownTarget(t *testing.T) {
	g := NewWithT(t)

	patches := []helm.Patch{
		{
			Target:   helm.PatchTarget{Kind: "Deployment", Name: "does-not-exist"},
			JSON6902: `[{"op": "replace", "path": "/spec/replicas", "value": 3}]`,
		},
	}

	pr, err := helm.NewKustomizePostRenderer(patches, "kube-system")
	g.Expect(err).NotTo(HaveOccurred())

	_, err = pr.Run(bytes.NewBufferString(testManifest))
	g.Expect(err).To(HaveOccurred(), "patching a non-existent target should fail with a clear error")
}

func TestKustomizePostRenderer_InvalidPatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		patches []helm.Patch
	}{
		{
			name: "missing kind",
			patches: []helm.Patch{
				{Target: helm.PatchTarget{Name: "cilium"}, StrategicMerge: "{}"},
			},
		},
		{
			name: "missing content",
			patches: []helm.Patch{
				{Target: helm.PatchTarget{Kind: "DaemonSet", Name: "cilium"}},
			},
		},
		{
			name: "both strategic-merge and json6902 set",
			patches: []helm.Patch{
				{
					Target:         helm.PatchTarget{Kind: "DaemonSet", Name: "cilium"},
					StrategicMerge: "{}",
					JSON6902:       "[]",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := helm.NewKustomizePostRenderer(tc.patches, "kube-system")
			g.Expect(err).To(HaveOccurred())
		})
	}
}
