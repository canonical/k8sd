package coredns_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/client/helm/helmtest"
	helmmock "github.com/canonical/k8sd/pkg/client/helm/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/coredns"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func TestDisabled(t *testing.T) {
	t.Run("HelmApplyFails", func(t *testing.T) {
		g := NewWithT(t)

		applyErr := errors.New("failed to apply")
		helmM := &helmmock.Mock{
			ApplyErr: applyErr,
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		dns := types.DNS{
			Enabled: ptr.To(false),
		}
		kubelet := types.Kubelet{}

		status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

		g.Expect(err).To(MatchError(ContainSubstring(applyErr.Error())))
		g.Expect(str).To(BeEmpty())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Message).To(ContainSubstring("failed to uninstall coredns"))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Version).To(Equal(coredns.ImageTag))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(coredns.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StateDeleted))
		g.Expect(callArgs.Values).To(BeNil())
	})
	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		dns := types.DNS{
			Enabled: ptr.To(false),
		}
		kubelet := types.Kubelet{}

		status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(str).To(BeEmpty())
		g.Expect(status.Message).To(Equal("disabled"))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Version).To(Equal(coredns.ImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(coredns.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StateDeleted))
		g.Expect(callArgs.Values).To(BeNil())
	})
}

func TestEnabled(t *testing.T) {
	t.Run("HelmApplyFails", func(t *testing.T) {
		g := NewWithT(t)

		applyErr := errors.New("failed to apply")
		helmM := &helmmock.Mock{
			ApplyErr: applyErr,
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		dns := types.DNS{
			Enabled: ptr.To(true),
		}
		kubelet := types.Kubelet{}

		status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

		g.Expect(err).To(MatchError(ContainSubstring(applyErr.Error())))
		g.Expect(str).To(BeEmpty())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Message).To(ContainSubstring("failed to apply coredns"))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Version).To(Equal(coredns.ImageTag))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(coredns.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StatePresent))
		validateValues(g, callArgs.Values, dns, kubelet)
	})
	t.Run("HelmApplySuccessServiceFails", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		clientset := fake.NewSimpleClientset()
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient:       helmM,
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}
		dns := types.DNS{
			Enabled: ptr.To(true),
		}
		kubelet := types.Kubelet{}

		status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

		g.Expect(err).To(MatchError(ContainSubstring("services \"coredns\" not found")))
		g.Expect(str).To(BeEmpty())
		g.Expect(status.Message).To(ContainSubstring("failed to retrieve the coredns service"))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Version).To(Equal(coredns.ImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(coredns.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StatePresent))
		validateValues(g, callArgs.Values, dns, kubelet)
	})
	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		clusterIp := "10.96.0.10"
		corednsService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "coredns",
				Namespace: "kube-system",
			},
			Spec: corev1.ServiceSpec{
				ClusterIP: clusterIp,
			},
		}
		clientset := fake.NewSimpleClientset(corednsService)
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient:       helmM,
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}
		dns := types.DNS{
			Enabled: ptr.To(true),
		}
		kubelet := types.Kubelet{}

		status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(str).To(Equal(clusterIp))
		g.Expect(status.Message).To(ContainSubstring("enabled at " + clusterIp))
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Version).To(Equal(coredns.ImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(coredns.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StatePresent))
		validateValues(g, callArgs.Values, dns, kubelet)
	})
}

func validateValues(g Gomega, values map[string]any, dns types.DNS, kubelet types.Kubelet) {
	g.Expect(values).To(HaveKeyWithValue("rollingUpdate", map[string]any{
		"maxSurge":       1,
		"maxUnavailable": 0,
	}))

	service := values["service"].(map[string]any)
	g.Expect(service["clusterIP"]).To(Equal(kubelet.GetClusterDNS()))

	servers := values["servers"].([]map[string]any)
	plugins := servers[0]["plugins"].([]map[string]any)
	g.Expect(plugins[3]["parameters"]).To(ContainSubstring(kubelet.GetClusterDomain()))
	g.Expect(plugins[5]["parameters"]).To(ContainSubstring(strings.Join(dns.GetUpstreamNameservers(), " ")))
	zones := servers[0]["zones"].([]map[string]any)
	g.Expect(zones[0]["zone"]).To(Equal("."))
	g.Expect(zones[0]["use_tcp"]).To(BeTrue())

	// Validate PriorityClass
	g.Expect(values["priorityClassName"]).To(Equal("system-node-critical"))

	// Validate tolerations
	tolerations, ok := values["tolerations"].([]corev1.Toleration)
	g.Expect(ok).To(BeTrue())
	g.Expect(tolerations).To(HaveLen(1))

	toleration := tolerations[0]
	g.Expect(toleration.Key).To(Equal("node-role.kubernetes.io/control-plane"))
	g.Expect(toleration.Operator).To(Equal(corev1.TolerationOpExists))
	g.Expect(toleration.Effect).To(Equal(corev1.TaintEffectNoSchedule))

	// Validate HPA configuration
	hpa := values["hpa"].(map[string]any)
	g.Expect(hpa["enabled"]).To(BeTrue())
	g.Expect(hpa["minReplicas"]).To(Equal(2))
	g.Expect(hpa["maxReplicas"]).To(Equal(100))

	metrics := hpa["metrics"].([]map[string]any)
	g.Expect(metrics).To(HaveLen(2))

	// CPU metric
	g.Expect(metrics[0]["type"]).To(Equal("Resource"))
	cpuResource := metrics[0]["resource"].(map[string]any)
	g.Expect(cpuResource["name"]).To(Equal("cpu"))
	cpuTarget := cpuResource["target"].(map[string]any)
	g.Expect(cpuTarget["type"]).To(Equal("Utilization"))
	g.Expect(cpuTarget["averageUtilization"]).To(Equal(80))

	// Memory metric
	g.Expect(metrics[1]["type"]).To(Equal("Resource"))
	memResource := metrics[1]["resource"].(map[string]any)
	g.Expect(memResource["name"]).To(Equal("memory"))
	memTarget := memResource["target"].(map[string]any)
	g.Expect(memTarget["type"]).To(Equal("Utilization"))
	g.Expect(memTarget["averageUtilization"]).To(Equal(70))

	// Validate PDB configuration
	pdb := values["podDisruptionBudget"].(map[string]any)
	g.Expect(pdb["minAvailable"]).To(Equal(1))

	// Validate PodAntiAffinity
	affinity := values["affinity"].(map[string]any)
	podAntiAffinity := affinity["podAntiAffinity"].(map[string]any)
	preferred := podAntiAffinity["preferredDuringSchedulingIgnoredDuringExecution"].([]map[string]any)
	g.Expect(preferred).To(HaveLen(1))
	g.Expect(preferred[0]["weight"]).To(Equal(100))

	podAffinityTerm := preferred[0]["podAffinityTerm"].(map[string]any)
	g.Expect(podAffinityTerm["topologyKey"]).To(Equal("kubernetes.io/hostname"))
	g.Expect(podAffinityTerm).To(HaveKeyWithValue("matchLabelKeys", []string{"pod-template-hash"}))
	labelSelector := podAffinityTerm["labelSelector"].(map[string]any)
	matchLabels := labelSelector["matchLabels"].(map[string]any)
	g.Expect(matchLabels["app.kubernetes.io/name"]).To(Equal("coredns"))
	g.Expect(matchLabels["app.kubernetes.io/instance"]).To(Equal("ck-dns"))

	// Validate TopologySpreadConstraints
	topologySpread := values["topologySpreadConstraints"].([]map[string]any)
	g.Expect(topologySpread).To(HaveLen(2))
	// Zone constraint
	zoneSelector := topologySpread[0]["labelSelector"].(map[string]any)
	zoneMatchLabels := zoneSelector["matchLabels"].(map[string]any)
	g.Expect(zoneMatchLabels["app.kubernetes.io/name"]).To(Equal("coredns"))
	g.Expect(zoneMatchLabels["app.kubernetes.io/instance"]).To(Equal("ck-dns"))
	g.Expect(zoneMatchLabels["k8s-app"]).To(Equal("coredns"))

	// Hostname constraint
	hostnameSelector := topologySpread[1]["labelSelector"].(map[string]any)
	hostnameMatchLabels := hostnameSelector["matchLabels"].(map[string]any)
	g.Expect(hostnameMatchLabels["app.kubernetes.io/name"]).To(Equal("coredns"))
	g.Expect(hostnameMatchLabels["app.kubernetes.io/instance"]).To(Equal("ck-dns"))
	g.Expect(hostnameMatchLabels["k8s-app"]).To(Equal("coredns"))

	// Zone constraint
	g.Expect(topologySpread[0]["maxSkew"]).To(Equal(1))
	g.Expect(topologySpread[0]["topologyKey"]).To(Equal("topology.kubernetes.io/zone"))
	g.Expect(topologySpread[0]["whenUnsatisfiable"]).To(Equal("ScheduleAnyway"))
	zoneMatchLabelKeys, ok := topologySpread[0]["matchLabelKeys"].([]string)
	g.Expect(ok).To(BeTrue())
	g.Expect(zoneMatchLabelKeys).To(Equal([]string{"pod-template-hash"}))

	// Hostname constraint
	g.Expect(topologySpread[1]["maxSkew"]).To(Equal(1))
	g.Expect(topologySpread[1]["topologyKey"]).To(Equal("kubernetes.io/hostname"))
	g.Expect(topologySpread[1]["whenUnsatisfiable"]).To(Equal("ScheduleAnyway"))
	hostnameMatchLabelKeys, ok := topologySpread[1]["matchLabelKeys"].([]string)
	g.Expect(ok).To(BeTrue())
	g.Expect(hostnameMatchLabelKeys).To(Equal([]string{"pod-template-hash"}))
}

func writeTestCoreDNSChart(t *testing.T, baseDir, versionStr string) {
	t.Helper()
	chartDir := filepath.Join(baseDir, coredns.Chart.ManifestPath)
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := "apiVersion: v2\nname: ck-dns\nversion: " + versionStr + "\ntype: application\n"
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	valuesYAML := "service:\n  name: coredns\n  clusterIP: \"\"\n"
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	svcTemplate := `apiVersion: v1
kind: Service
metadata:
  name: {{ .Values.service.name }}
  namespace: {{ .Release.Namespace }}
spec:
{{- if .Values.service.clusterIP }}
  clusterIP: {{ .Values.service.clusterIP }}
{{- end }}
  ports:
  - port: 53
    name: dns
`
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "service.yaml"), []byte(svcTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
}

func corednsServiceWithIP(ip string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: ip,
		},
	}
}

func TestApplyDNS_DynamicBootstrap_SecondReconcileKeepsRevision1(t *testing.T) {
	g := NewWithT(t)
	baseDir := t.TempDir()
	writeTestCoreDNSChart(t, baseDir, "1.47.0")

	helmClient, store := helmtest.NewMemoryClient(baseDir, "kube-system")

	liveIP := "10.152.183.211"
	clientset := fake.NewSimpleClientset(corednsServiceWithIP(liveIP))
	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmClient,
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}

	dns := types.DNS{Enabled: ptr.To(true)}
	kubelet := types.Kubelet{} // ClusterDNS == ""

	// First reconcile installs CoreDNS with a dynamically assigned clusterIP.
	status, ip, err := coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(liveIP))
	g.Expect(status.Enabled).To(BeTrue())

	rel, err := store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	relObj := rel.(*releasev1.Release)
	g.Expect(relObj.Version).To(Equal(1))
	g.Expect(relObj.Config["service"].(map[string]any)["clusterIP"]).To(Equal(""))

	// Second reconcile sees the assigned address fed back into the cluster config.
	kubelet.ClusterDNS = ptr.To(liveIP)
	status, ip, err = coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(liveIP))
	g.Expect(status.Enabled).To(BeTrue())

	rel, err = store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rel.(*releasev1.Release).Version).To(Equal(1))

	// A real configuration change still upgrades, keeping the dynamic clusterIP.
	writeTestCoreDNSChart(t, baseDir, "1.48.0")
	dns.UpstreamNameservers = ptr.To([]string{"1.1.1.1", "8.8.8.8"})
	_, _, err = coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())

	rel, err = store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	relObj = rel.(*releasev1.Release)
	g.Expect(relObj.Version).To(Equal(2))
	g.Expect(relObj.Chart.Metadata.Version).To(Equal("1.48.0"))
	g.Expect(relObj.Config["service"].(map[string]any)["clusterIP"]).To(Equal(""))

	// No further changes, no further revisions.
	_, _, err = coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())

	rel, err = store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rel.(*releasev1.Release).Version).To(Equal(2))
}

func TestApplyDNS_NullAndOmittedClusterIP_KeepsRevision1(t *testing.T) {
	liveIP := "10.152.183.211"

	for _, tc := range []struct {
		name       string
		mutateSvc  func(svc map[string]any)
		configured string
		expectNull bool
	}{
		{
			name:       "null clusterIP in deployed config",
			mutateSvc:  func(svc map[string]any) { svc["clusterIP"] = nil },
			configured: liveIP,
			expectNull: true,
		},
		{
			name:       "omitted clusterIP in deployed config",
			mutateSvc:  func(svc map[string]any) { delete(svc, "clusterIP") },
			configured: liveIP,
		},
		{
			name:       "ipv4-mapped ipv6 clusterDNS matches live ipv4 service",
			mutateSvc:  func(svc map[string]any) { delete(svc, "clusterIP") },
			configured: "::ffff:10.152.183.211",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			baseDir := t.TempDir()
			writeTestCoreDNSChart(t, baseDir, "1.47.0")

			helmClient, store := helmtest.NewMemoryClient(baseDir, "kube-system")
			clientset := fake.NewSimpleClientset(corednsServiceWithIP(liveIP))
			snapM := &snapmock.Snap{
				Mock: snapmock.Mock{
					HelmClient:       helmClient,
					KubernetesClient: &kubernetes.Client{Interface: clientset},
				},
			}

			dns := types.DNS{Enabled: ptr.To(true)}
			_, _, err := coredns.ApplyDNS(t.Context(), snapM, dns, types.Kubelet{}, nil)
			g.Expect(err).NotTo(HaveOccurred())

			rel, err := store.Last(coredns.Chart.Name)
			g.Expect(err).NotTo(HaveOccurred())
			relObj := rel.(*releasev1.Release)
			tc.mutateSvc(relObj.Config["service"].(map[string]any))
			g.Expect(store.Update(relObj)).To(Succeed())

			status, ip, err := coredns.ApplyDNS(t.Context(), snapM, dns, types.Kubelet{
				ClusterDNS: ptr.To(tc.configured),
			}, nil)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ip).To(Equal(liveIP))
			g.Expect(status.Enabled).To(BeTrue())

			rel, err = store.Last(coredns.Chart.Name)
			g.Expect(err).NotTo(HaveOccurred())
			updatedRel := rel.(*releasev1.Release)
			g.Expect(updatedRel.Version).To(Equal(1))
			if tc.expectNull {
				g.Expect(updatedRel.Config["service"].(map[string]any)).To(HaveKeyWithValue("clusterIP", BeNil()))
			} else {
				g.Expect(updatedRel.Config["service"].(map[string]any)).NotTo(HaveKey("clusterIP"))
			}
		})
	}
}

func TestApplyDNS_ClusterIPValuePassedToHelm(t *testing.T) {
	desiredIP := "10.152.183.211"

	for _, tc := range []struct {
		name       string
		deployedIP string
		service    *corev1.Service
		expectIP   string
	}{
		{
			name:       "dynamic release and live service matches keeps empty clusterIP",
			deployedIP: "",
			service:    corednsServiceWithIP(desiredIP),
			expectIP:   "",
		},
		{
			name:       "explicit release clusterIP is kept",
			deployedIP: desiredIP,
			service:    corednsServiceWithIP(desiredIP),
			expectIP:   desiredIP,
		},
		{
			name:       "dynamic release with different live address applies desired clusterIP",
			deployedIP: "",
			service:    corednsServiceWithIP("10.152.183.99"),
			expectIP:   desiredIP,
		},
		{
			name:       "dynamic release with missing service applies desired clusterIP",
			deployedIP: "",
			expectIP:   desiredIP,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			helmM := &helmmock.Mock{
				GetDeployedConfigDeployed: true,
				GetDeployedConfigResult: map[string]any{
					"service": map[string]any{"clusterIP": tc.deployedIP},
				},
			}
			clientset := fake.NewSimpleClientset()
			if tc.service != nil {
				clientset = fake.NewSimpleClientset(tc.service)
			}
			snapM := &snapmock.Snap{
				Mock: snapmock.Mock{
					HelmClient:       helmM,
					KubernetesClient: &kubernetes.Client{Interface: clientset},
				},
			}

			_, _, _ = coredns.ApplyDNS(t.Context(), snapM, types.DNS{Enabled: ptr.To(true)}, types.Kubelet{
				ClusterDNS: ptr.To(desiredIP),
			}, nil)

			g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))
			svcVal, ok := helmM.ApplyCalledWith[0].Values["service"].(map[string]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(svcVal).To(HaveKeyWithValue("clusterIP", tc.expectIP))
		})
	}
}

func TestApplyDNS_LookupErrors_FailWithoutApplying(t *testing.T) {
	clusterIP := "10.152.183.211"

	for _, tc := range []struct {
		name        string
		helmM       *helmmock.Mock
		clientset   func() *fake.Clientset
		expectedErr string
	}{
		{
			name: "transient kubernetes error",
			helmM: &helmmock.Mock{
				GetDeployedConfigDeployed: true,
				GetDeployedConfigResult: map[string]any{
					"service": map[string]any{"clusterIP": ""},
				},
			},
			clientset: func() *fake.Clientset {
				c := fake.NewSimpleClientset()
				c.PrependReactor("get", "services", func(clientgotesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewInternalError(errors.New("apiserver timeout"))
				})
				return c
			},
			expectedErr: "failed to retrieve the coredns service",
		},
		{
			name: "deployed release lookup error",
			helmM: &helmmock.Mock{
				GetDeployedConfigErr: errors.New("helm storage failure"),
			},
			clientset: func() *fake.Clientset {
				return fake.NewSimpleClientset(corednsServiceWithIP(clusterIP))
			},
			expectedErr: "failed to get deployed release config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			snapM := &snapmock.Snap{
				Mock: snapmock.Mock{
					HelmClient:       tc.helmM,
					KubernetesClient: &kubernetes.Client{Interface: tc.clientset()},
				},
			}

			status, ip, err := coredns.ApplyDNS(context.Background(), snapM, types.DNS{Enabled: ptr.To(true)}, types.Kubelet{
				ClusterDNS: ptr.To(clusterIP),
			}, nil)

			g.Expect(err).To(MatchError(ContainSubstring(tc.expectedErr)))
			g.Expect(ip).To(BeEmpty())
			g.Expect(status.Enabled).To(BeFalse())
			g.Expect(tc.helmM.ApplyCalledWith).To(BeEmpty())
		})
	}
}
