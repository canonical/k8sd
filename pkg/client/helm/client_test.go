package helm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
)

func writeTestChart(t *testing.T, baseDir, relDir, version string) InstallableChart {
	t.Helper()
	chartDir := filepath.Join(baseDir, relDir)
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := "apiVersion: v2\nname: test-chart\nversion: " + version + "\n"
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	valuesYAML := "defaultKey: from-chart\ndevices: null\n"
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cmTemplate := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  namespace: {{ .Release.Namespace }}
data:
  values: {{ .Values | toJson | quote }}
`
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "configmap.yaml"), []byte(cmTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
	return InstallableChart{
		Name:         "test-release",
		Namespace:    "kube-system",
		ManifestPath: relDir,
	}
}

func newMemoryHelmClient(t *testing.T, baseDir string) (*client, *storage.Storage) {
	t.Helper()
	mem := driver.NewMemory()
	mem.SetNamespace("kube-system")
	store := storage.Init(mem)

	c := &client{
		manifestsBaseDir: baseDir,
		timeout:          time.Minute,
		maxHistory:       10,
		newConfig: func(_ context.Context, _ string) (*action.Configuration, error) {
			return &action.Configuration{
				Releases:     store,
				Capabilities: chartutil.DefaultCapabilities.Copy(),
				KubeClient: &kubefake.PrintingKubeClient{
					Out: io.Discard,
				},
				Log: func(string, ...any) {},
			}, nil
		},
	}
	return c, store
}

func TestApply_SingleOwnerDropsRemovedAndOutOfBandValues(t *testing.T) {
	g := NewWithT(t)
	baseDir := t.TempDir()
	chart := writeTestChart(t, baseDir, "charts/test", "0.1.0")
	c, store := newMemoryHelmClient(t, baseDir)

	changed, err := c.Apply(t.Context(), chart, StatePresent, map[string]any{
		"foo":   "bar",
		"stale": "present-initially",
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeTrue())

	// Second apply omits "stale": single-owner chart must reset removed keys instead of resurrecting them.
	changed, err = c.Apply(t.Context(), chart, StatePresent, map[string]any{
		"foo": "bar",
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeTrue())

	rel, err := store.Last(chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rel.Version).To(Equal(2))
	g.Expect(rel.Config).To(Equal(map[string]any{"foo": "bar"}))

	// Identical subsequent reconcile must be a no-op with no extra Helm revision.
	changed, err = c.Apply(t.Context(), chart, StatePresent, map[string]any{
		"foo": "bar",
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeFalse())

	rel, err = store.Last(chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rel.Version).To(Equal(2))
}

func TestApply_NilValuesIdempotent(t *testing.T) {
	g := NewWithT(t)
	baseDir := t.TempDir()
	chart := writeTestChart(t, baseDir, "charts/nil-vals", "0.1.0")
	c, store := newMemoryHelmClient(t, baseDir)

	changed, err := c.Apply(t.Context(), chart, StatePresent, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeTrue())

	changed, err = c.Apply(t.Context(), chart, StatePresent, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeFalse())

	rel, err := store.Last(chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rel.Version).To(Equal(1))
}

func TestApply_RetainKeysPreservesSharedSubsectionsAndDropsRemovedKeys(t *testing.T) {
	g := NewWithT(t)
	baseDir := t.TempDir()
	chart := writeTestChart(t, baseDir, "charts/cilium", "1.20.0")
	chart.RetainKeys = []string{"ingressController", "gatewayAPI"}
	c, store := newMemoryHelmClient(t, baseDir)

	// 1. Network controller installs with "devices" annotation set.
	changed, err := c.Apply(t.Context(), chart, StatePresent, map[string]any{
		"kubeProxyReplacement": true,
		"devices":              "eth0",
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeTrue())

	// 2. Gateway controller enables gatewayAPI via StateUpgradeOnly.
	changed, err = c.Apply(t.Context(), chart, StateUpgradeOnly, map[string]any{
		"gatewayAPI": map[string]any{
			"enabled": true,
			"gatewayClass": map[string]any{
				"create": "false",
			},
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeTrue())

	// 3. Operator removes "devices" annotation; network controller reconciles without "devices".
	// "devices" must be dropped while "gatewayAPI" is preserved via RetainKeys.
	changed, err = c.Apply(t.Context(), chart, StatePresent, map[string]any{
		"kubeProxyReplacement": true,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeTrue())

	rel, err := store.Last(chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rel.Config).To(Equal(map[string]any{
		"kubeProxyReplacement": true,
		"gatewayAPI": map[string]any{
			"enabled": true,
			"gatewayClass": map[string]any{
				"create": "false",
			},
		},
	}))

	// 4. Gateway controller disables gatewayAPI via StateUpgradeOnly: replaces "gatewayAPI" wholesale.
	changed, err = c.Apply(t.Context(), chart, StateUpgradeOnly, map[string]any{
		"gatewayAPI": map[string]any{
			"enabled": false,
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeTrue())

	rel, err = store.Last(chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rel.Config).To(Equal(map[string]any{
		"kubeProxyReplacement": true,
		"gatewayAPI": map[string]any{
			"enabled": false,
		},
	}))

	// 5. Re-applying identical StateUpgradeOnly is a no-op.
	changed, err = c.Apply(t.Context(), chart, StateUpgradeOnly, map[string]any{
		"gatewayAPI": map[string]any{
			"enabled": false,
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeFalse())
}
