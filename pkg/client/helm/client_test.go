package helm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"helm.sh/helm/v4/pkg/action"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/scheme"
)

func writeTestChart(t *testing.T, baseDir, relDir, versionStr string) InstallableChart {
	t.Helper()
	chartDir := filepath.Join(baseDir, relDir)
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := "apiVersion: v2\nname: test-chart\nversion: " + versionStr + "\ntype: application\n"
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
			cfg := action.NewConfiguration(
				action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)),
			)
			cfg.Releases = store
			cfg.Capabilities = chartcommon.DefaultCapabilities.Copy()
			cfg.KubeClient = &kubefake.PrintingKubeClient{
				Out: io.Discard,
			}
			return cfg, nil
		},
	}
	return c, store
}

func mustLastRelease(t *testing.T, store *storage.Storage, name string) *releasev1.Release {
	t.Helper()
	rel, err := store.Last(name)
	if err != nil {
		t.Fatalf("store.Last(%q) failed: %v", name, err)
	}
	relV1, ok := rel.(*releasev1.Release)
	if !ok {
		t.Fatalf("expected *releasev1.Release, got %T", rel)
	}
	return relV1
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

	rel := mustLastRelease(t, store, chart.Name)
	g.Expect(rel.Version).To(Equal(2))
	g.Expect(rel.Config).To(Equal(map[string]any{"foo": "bar"}))

	// Identical subsequent reconcile must be a no-op with no extra Helm revision.
	changed, err = c.Apply(t.Context(), chart, StatePresent, map[string]any{
		"foo": "bar",
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(changed).To(BeFalse())

	rel = mustLastRelease(t, store, chart.Name)
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

	rel := mustLastRelease(t, store, chart.Name)
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

	rel := mustLastRelease(t, store, chart.Name)
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

	rel = mustLastRelease(t, store, chart.Name)
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

type fakeKubeAPIServer struct {
	mu         sync.Mutex
	secrets    map[string]*corev1.Secret
	configMaps map[string]*corev1.ConfigMap
}

func decodeBody(r *http.Request, into runtime.Object) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if strings.Contains(r.Header.Get("Content-Type"), "patch+json") {
		return json.Unmarshal(raw, into)
	}
	_, _, err = scheme.Codecs.UniversalDeserializer().Decode(raw, nil, into)
	return err
}

func newFakeKubeAPIServer(t *testing.T) (*httptest.Server, *fakeKubeAPIServer) {
	t.Helper()
	state := &fakeKubeAPIServer{
		secrets:    make(map[string]*corev1.Secret),
		configMaps: make(map[string]*corev1.ConfigMap),
	}

	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/version":
			writeJSON(w, http.StatusOK, version.Info{Major: "1", Minor: "37", GitVersion: "v1.37.2"})
		case r.URL.Path == "/api":
			writeJSON(w, http.StatusOK, metav1.APIVersions{
				TypeMeta: metav1.TypeMeta{Kind: "APIVersions", APIVersion: "v1"},
				Versions: []string{"v1"},
			})
		case r.URL.Path == "/api/v1":
			writeJSON(w, http.StatusOK, metav1.APIResourceList{
				TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "namespaces", SingularName: "namespace", Namespaced: false, Kind: "Namespace", Verbs: metav1.Verbs{"get", "list", "create", "update", "patch", "delete"}},
					{Name: "secrets", SingularName: "secret", Namespaced: true, Kind: "Secret", Verbs: metav1.Verbs{"get", "list", "create", "update", "patch", "delete"}},
					{Name: "configmaps", SingularName: "configmap", Namespaced: true, Kind: "ConfigMap", Verbs: metav1.Verbs{"get", "list", "create", "update", "patch", "delete"}},
				},
			})
		case r.URL.Path == "/apis":
			writeJSON(w, http.StatusOK, metav1.APIGroupList{
				TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"},
			})
		case r.URL.Path == "/openapi/v2":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0x0a, 0x03, '2', '.', '0'})
		case r.URL.Path == "/openapi/v3":
			writeJSON(w, http.StatusNotFound, metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusFailure, Code: http.StatusNotFound})
		case strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/") && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"), "/"):
			ns := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/")
			writeJSON(w, http.StatusOK, corev1.Namespace{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
				ObjectMeta: metav1.ObjectMeta{Name: ns},
			})
		case r.URL.Path == "/api/v1/namespaces":
			var ns corev1.Namespace
			if err := decodeBody(r, &ns); err != nil {
				writeJSON(w, http.StatusBadRequest, apierrors.NewBadRequest(err.Error()).ErrStatus)
				return
			}
			ns.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"}
			writeJSON(w, http.StatusCreated, ns)
		case strings.Contains(r.URL.Path, "/secrets"):
			state.handleSecrets(w, r, writeJSON)
		case strings.Contains(r.URL.Path, "/configmaps"):
			state.handleConfigMaps(w, r, writeJSON)
		default:
			writeJSON(w, http.StatusNotFound, apierrors.NewNotFound(schema.GroupResource{Resource: r.URL.Path}, "").ErrStatus)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func (s *fakeKubeAPIServer) handleSecrets(w http.ResponseWriter, r *http.Request, writeJSON func(http.ResponseWriter, int, any)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"), "/")
	ns := parts[0]

	switch {
	case len(parts) == 2 && r.Method == http.MethodGet:
		sel, _ := labels.Parse(r.URL.Query().Get("labelSelector"))
		var items []corev1.Secret
		for k, sec := range s.secrets {
			if strings.HasPrefix(k, ns+"/") && (sel == nil || sel.Matches(labels.Set(sec.Labels))) {
				items = append(items, *sec.DeepCopy())
			}
		}
		writeJSON(w, http.StatusOK, corev1.SecretList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "SecretList"},
			Items:    items,
		})
	case len(parts) == 2 && r.Method == http.MethodPost:
		var sec corev1.Secret
		if err := decodeBody(r, &sec); err != nil {
			writeJSON(w, http.StatusBadRequest, apierrors.NewBadRequest(err.Error()).ErrStatus)
			return
		}
		sec.Namespace = ns
		sec.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}
		s.secrets[ns+"/"+sec.Name] = &sec
		writeJSON(w, http.StatusCreated, sec)
	case len(parts) == 3 && r.Method == http.MethodGet:
		name := parts[2]
		sec, ok := s.secrets[ns+"/"+name]
		if !ok {
			writeJSON(w, http.StatusNotFound, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name).ErrStatus)
			return
		}
		writeJSON(w, http.StatusOK, sec.DeepCopy())
	case len(parts) == 3 && r.Method == http.MethodPut:
		name := parts[2]
		var sec corev1.Secret
		if err := decodeBody(r, &sec); err != nil {
			writeJSON(w, http.StatusBadRequest, apierrors.NewBadRequest(err.Error()).ErrStatus)
			return
		}
		sec.Namespace = ns
		sec.Name = name
		sec.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}
		s.secrets[ns+"/"+name] = &sec
		writeJSON(w, http.StatusOK, sec)
	case len(parts) == 3 && r.Method == http.MethodDelete:
		name := parts[2]
		if _, ok := s.secrets[ns+"/"+name]; !ok {
			writeJSON(w, http.StatusNotFound, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name).ErrStatus)
			return
		}
		delete(s.secrets, ns+"/"+name)
		writeJSON(w, http.StatusOK, metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusSuccess})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusFailure, Code: http.StatusMethodNotAllowed})
	}
}

func (s *fakeKubeAPIServer) handleConfigMaps(w http.ResponseWriter, r *http.Request, writeJSON func(http.ResponseWriter, int, any)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"), "/")
	ns := parts[0]

	switch {
	case len(parts) == 2 && r.Method == http.MethodPost:
		var cm corev1.ConfigMap
		if err := decodeBody(r, &cm); err != nil {
			writeJSON(w, http.StatusBadRequest, apierrors.NewBadRequest(err.Error()).ErrStatus)
			return
		}
		cm.Namespace = ns
		cm.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
		s.configMaps[ns+"/"+cm.Name] = &cm
		writeJSON(w, http.StatusCreated, cm)
	case len(parts) == 3 && r.Method == http.MethodGet:
		name := parts[2]
		cm, ok := s.configMaps[ns+"/"+name]
		if !ok {
			writeJSON(w, http.StatusNotFound, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name).ErrStatus)
			return
		}
		writeJSON(w, http.StatusOK, cm.DeepCopy())
	case len(parts) == 3 && (r.Method == http.MethodPatch || r.Method == http.MethodPut):
		name := parts[2]
		cm, ok := s.configMaps[ns+"/"+name]
		if !ok {
			cm = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
		}
		var patch corev1.ConfigMap
		if err := decodeBody(r, &patch); err != nil {
			writeJSON(w, http.StatusBadRequest, apierrors.NewBadRequest(err.Error()).ErrStatus)
			return
		}
		if patch.Data != nil {
			cm.Data = patch.Data
		}
		if patch.Labels != nil {
			cm.Labels = patch.Labels
		}
		if patch.Annotations != nil {
			cm.Annotations = patch.Annotations
		}
		cm.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
		s.configMaps[ns+"/"+name] = cm
		writeJSON(w, http.StatusOK, cm.DeepCopy())
	case len(parts) == 3 && r.Method == http.MethodDelete:
		name := parts[2]
		if _, ok := s.configMaps[ns+"/"+name]; !ok {
			writeJSON(w, http.StatusNotFound, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name).ErrStatus)
			return
		}
		delete(s.configMaps, ns+"/"+name)
		writeJSON(w, http.StatusOK, metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusSuccess})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusFailure, Code: http.StatusMethodNotAllowed})
	}
}

func TestClientApplyLifecycleWithRealKubeClient(t *testing.T) {
	srv, state := newFakeKubeAPIServer(t)
	baseDir := t.TempDir()
	chart := writeTestChart(t, baseDir, "charts/test-addon", "0.1.0")

	getter := func(ns string) genericclioptions.RESTClientGetter {
		flags := genericclioptions.NewConfigFlags(false)
		serverURL := srv.URL
		flags.APIServer = &serverURL
		flags.Namespace = &ns
		return flags
	}

	c := NewClient(baseDir, getter, 30*time.Second, 5)
	ctx := t.Context()

	// 1. Install when not present (exercises real *kube.Client.GetWaiterWithOptions)
	changed, err := c.Apply(ctx, chart, StatePresent, map[string]any{"setting": "v1"})
	if err != nil {
		t.Fatalf("Apply(StatePresent) install failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on initial install")
	}
	if _, ok := state.configMaps["kube-system/test-cm"]; !ok {
		t.Fatalf("expected ConfigMap test-cm to be created")
	}

	// 2. Idempotent no-op when values and chart version are unchanged
	changed, err = c.Apply(ctx, chart, StatePresent, map[string]any{"setting": "v1"})
	if err != nil {
		t.Fatalf("Apply(StatePresent) idempotent check failed: %v", err)
	}
	if changed {
		t.Fatalf("expected changed=false when values and version are unchanged")
	}

	// 3. Upgrade when values change
	changed, err = c.Apply(ctx, chart, StatePresent, map[string]any{"setting": "v2"})
	if err != nil {
		t.Fatalf("Apply(StatePresent) upgrade on values change failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when values changed")
	}

	// 4. Upgrade when chart version changes
	chart = writeTestChart(t, baseDir, "charts/test-addon", "0.2.0")
	changed, err = c.Apply(ctx, chart, StateUpgradeOnly, map[string]any{"setting": "v2"})
	if err != nil {
		t.Fatalf("Apply(StateUpgradeOnly) upgrade on version bump failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when chart version changed")
	}

	// 5. Recover from wedged pending-upgrade release state
	cfg, err := c.newActionConfiguration(ctx, chart.Namespace)
	if err != nil {
		t.Fatalf("newActionConfiguration failed: %v", err)
	}
	relV1 := mustLastRelease(t, cfg.Releases, chart.Name)
	relV1.Info.Status = releasecommon.StatusPendingUpgrade
	if err := cfg.Releases.Update(relV1); err != nil {
		t.Fatalf("Releases.Update(pending) failed: %v", err)
	}

	changed, err = c.Apply(ctx, chart, StatePresent, map[string]any{"setting": "v2"})
	if err != nil {
		t.Fatalf("Apply(StatePresent) recovery from pending state failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when recovering pending release")
	}

	// 6. Uninstall when StateDeleted
	changed, err = c.Apply(ctx, chart, StateDeleted, nil)
	if err != nil {
		t.Fatalf("Apply(StateDeleted) uninstall failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on uninstall")
	}
	if _, exists := state.configMaps["kube-system/test-cm"]; exists {
		t.Fatalf("expected ConfigMap test-cm to be deleted on uninstall")
	}

	// 7. Uninstall when already deleted is a no-op
	changed, err = c.Apply(ctx, chart, StateDeleted, nil)
	if err != nil {
		t.Fatalf("Apply(StateDeleted) idempotent delete failed: %v", err)
	}
	if changed {
		t.Fatalf("expected changed=false when already uninstalled")
	}
}
