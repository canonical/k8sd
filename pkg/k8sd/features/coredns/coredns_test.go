package coredns_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/client/helm/helmtest"
	helmmock "github.com/canonical/k8sd/pkg/client/helm/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/coredns"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
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

func TestApplyDNS_DynamicBootstrap_SecondReconcileKeepsRevision1(t *testing.T) {
	g := NewWithT(t)
	baseDir := t.TempDir()
	writeTestCoreDNSChart(t, baseDir, "1.47.0")

	helmClient, store := helmtest.NewMemoryClient(baseDir, "kube-system")

	liveIP := "10.152.183.211"
	corednsService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: liveIP,
		},
	}
	clientset := fake.NewSimpleClientset(corednsService)
	k8sClient := &kubernetes.Client{Interface: clientset}

	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmClient,
			KubernetesClient: k8sClient,
		},
	}

	dns := types.DNS{Enabled: ptr.To(true)}
	kubelet := types.Kubelet{} // ClusterDNS == ""

	// 1. First reconcile: bootstrap with ClusterDNS == ""
	status, ip, err := coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(liveIP))
	g.Expect(status.Enabled).To(BeTrue())

	rel, err := store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	relObj := rel.(*releasev1.Release)
	g.Expect(relObj.Version).To(Equal(1))
	g.Expect(relObj.Chart.Metadata.Version).To(Equal("1.47.0"))
	g.Expect(relObj.Config["service"].(map[string]any)["clusterIP"]).To(Equal(""))

	// 2. Second reconcile: Kubelet has learned dynamic IP (ClusterDNS == "10.152.183.211")
	kubelet.ClusterDNS = ptr.To(liveIP)
	status, ip, err = coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(liveIP))
	g.Expect(status.Enabled).To(BeTrue())

	// MUST STAY REVISION 1!
	rel, err = store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	relObj = rel.(*releasev1.Release)
	g.Expect(relObj.Version).To(Equal(1))

	// 3. Third reconcile: actual configuration change (UpstreamNameservers changed & Chart bumped to 1.48.0)
	writeTestCoreDNSChart(t, baseDir, "1.48.0")
	dns.UpstreamNameservers = ptr.To([]string{"1.1.1.1", "8.8.8.8"})
	status, ip, err = coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(liveIP))
	g.Expect(status.Enabled).To(BeTrue())

	// Must upgrade to Revision 2
	rel, err = store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	relObj = rel.(*releasev1.Release)
	g.Expect(relObj.Version).To(Equal(2))
	g.Expect(relObj.Chart.Metadata.Version).To(Equal("1.48.0"))
	g.Expect(relObj.Config["service"].(map[string]any)["clusterIP"]).To(Equal(""))

	// 4. Fourth reconcile: no changes after upgrade, stays Revision 2
	status, ip, err = coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(liveIP))

	rel, err = store.Last(coredns.Chart.Name)
	g.Expect(err).NotTo(HaveOccurred())
	relObj = rel.(*releasev1.Release)
	g.Expect(relObj.Version).To(Equal(2))
}

func TestApplyDNS_NullAndOmittedClusterIP_KeepsRevision1(t *testing.T) {
	liveIP := "10.152.183.211"

	for _, tc := range []struct {
		name       string
		mutateSvc  func(svc map[string]any)
		configured string
	}{
		{
			name: "null clusterIP in deployed config",
			mutateSvc: func(svc map[string]any) {
				svc["clusterIP"] = nil
			},
			configured: liveIP,
		},
		{
			name: "omitted clusterIP in deployed config",
			mutateSvc: func(svc map[string]any) {
				delete(svc, "clusterIP")
			},
			configured: liveIP,
		},
		{
			name: "ipv4-mapped ipv6 clusterDNS matches live ipv4 service",
			mutateSvc: func(svc map[string]any) {
				delete(svc, "clusterIP")
			},
			configured: "::ffff:10.152.183.211",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			baseDir := t.TempDir()
			writeTestCoreDNSChart(t, baseDir, "1.47.0")

			helmClient, store := helmtest.NewMemoryClient(baseDir, "kube-system")
			corednsService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "coredns",
					Namespace: "kube-system",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: liveIP,
				},
			}
			clientset := fake.NewSimpleClientset(corednsService)
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
			if tc.name != "null clusterIP in deployed config" {
				g.Expect(updatedRel.Config["service"].(map[string]any)).NotTo(HaveKey("clusterIP"))
			} else {
				g.Expect(updatedRel.Config["service"].(map[string]any)).To(HaveKeyWithValue("clusterIP", BeNil()))
			}
		})
	}
}

func TestApplyDNS_RealHelmClient_MissingServiceRecovery(t *testing.T) {
	g := NewWithT(t)
	baseDir := t.TempDir()
	writeTestCoreDNSChart(t, baseDir, "1.47.0")

	helmClient, _ := helmtest.NewMemoryClient(baseDir, "kube-system")

	allocatedIP := "10.152.183.211"
	corednsService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: allocatedIP,
		},
	}
	clientset := fake.NewSimpleClientset(corednsService)
	k8sClient := &kubernetes.Client{Interface: clientset}

	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmClient,
			KubernetesClient: k8sClient,
		},
	}

	dns := types.DNS{Enabled: ptr.To(true)}
	kubelet := types.Kubelet{}

	// Initial bootstrap
	_, _, err := coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())

	// Simulate out-of-band deletion of Service/coredns
	err = clientset.CoreV1().Services("kube-system").Delete(t.Context(), "coredns", metav1.DeleteOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	// Prepend reactor to simulate service recreated after reinstall
	callCount := 0
	clientset.PrependReactor("get", "services", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		callCount++
		if callCount <= 2 {
			// Pre-apply and post-lease checks: Service is missing (NotFound)
			return true, nil, apierrors.NewNotFound(corev1.Resource("services"), "coredns")
		}
		// Post-apply check: Service was recreated
		return true, corednsService, nil
	})

	kubelet.ClusterDNS = ptr.To(allocatedIP)
	status, ip, err := coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(allocatedIP))
	g.Expect(status.Enabled).To(BeTrue())
}

func TestApplyDNS_RealHelmClient_DriftedServiceRecovery(t *testing.T) {
	g := NewWithT(t)
	baseDir := t.TempDir()
	writeTestCoreDNSChart(t, baseDir, "1.47.0")

	helmClient, _ := helmtest.NewMemoryClient(baseDir, "kube-system")

	allocatedIP := "10.152.183.211"
	corednsService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: allocatedIP,
		},
	}
	clientset := fake.NewSimpleClientset(corednsService)
	k8sClient := &kubernetes.Client{Interface: clientset}

	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmClient,
			KubernetesClient: k8sClient,
		},
	}

	dns := types.DNS{Enabled: ptr.To(true)}
	kubelet := types.Kubelet{}

	// Initial bootstrap
	_, _, err := coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())

	// Simulate service IP drift in Kubernetes to a different IP (e.g. 10.152.183.99)
	driftedService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.152.183.99",
		},
	}

	// Prepend reactor: pre-apply check sees drifted IP; post-apply check sees recreated service
	callCount := 0
	clientset.PrependReactor("get", "services", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		callCount++
		if callCount <= 2 {
			return true, driftedService, nil
		}
		return true, corednsService, nil
	})

	kubelet.ClusterDNS = ptr.To(allocatedIP)
	status, ip, err := coredns.ApplyDNS(t.Context(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(allocatedIP))
	g.Expect(status.Enabled).To(BeTrue())
}

func TestApplyDNS_ConcurrentPeerAlreadyRepairedService_SkipsUninstall(t *testing.T) {
	g := NewWithT(t)

	allocatedIP := "10.152.183.211"
	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"name":      "coredns",
				"clusterIP": "",
			},
		},
	}

	repairedService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: allocatedIP,
		},
	}
	clientset := fake.NewSimpleClientset()
	svcGets := 0
	clientset.PrependReactor("get", "services", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		svcGets++
		if svcGets == 1 {
			// Initial unlocked check observes stale NotFound before peer finishes repairing.
			return true, nil, apierrors.NewNotFound(corev1.Resource("services"), "coredns")
		}
		// Re-check under recovery lease observes the peer's newly repaired Service.
		return true, repairedService, nil
	})

	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmM,
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}

	status, ip, err := coredns.ApplyDNS(
		t.Context(),
		snapM,
		types.DNS{Enabled: ptr.To(true)},
		types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
		nil,
	)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(allocatedIP))
	g.Expect(status.Enabled).To(BeTrue())

	// Must NOT have called StateDeleted; only a single StatePresent preserving ""
	g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))
	g.Expect(helmM.ApplyCalledWith[0].State).To(Equal(helm.StatePresent))
	g.Expect(helmM.ApplyCalledWith[0].Values["service"].(map[string]any)["clusterIP"]).To(Equal(""))
}

func TestApplyDNS_ActiveRecoveryLeaseHeldByPeer_AbortsWithoutUninstall(t *testing.T) {
	g := NewWithT(t)

	allocatedIP := "10.152.183.211"
	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"name":      "coredns",
				"clusterIP": allocatedIP,
			},
		},
	}

	now := metav1.NewMicroTime(time.Now())
	activeLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ck-dns-recovery",
			Namespace: "kube-system",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("peer-cp-node"),
			LeaseDurationSeconds: ptr.To(int32(360)),
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}
	clientset := fake.NewSimpleClientset(activeLease)
	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmM,
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}

	status, ip, err := coredns.ApplyDNS(
		t.Context(),
		snapM,
		types.DNS{Enabled: ptr.To(true)},
		types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
		nil,
	)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("recovery is already in progress"))
	g.Expect(ip).To(BeEmpty())
	g.Expect(status.Enabled).To(BeFalse())
	g.Expect(helmM.ApplyCalledWith).To(BeEmpty(), "must not uninstall or apply while peer holds active recovery lease")
}

func TestApplyDNS_ExpiredRecoveryLease_StealsLeaseAndRecoversService(t *testing.T) {
	g := NewWithT(t)
	coredns.ResetObservedRecoveryLeases()
	t.Cleanup(coredns.ResetObservedRecoveryLeases)

	origDuration := coredns.RecoveryLeaseDuration
	origInterval := coredns.RecoveryLeaseRenewInterval
	coredns.RecoveryLeaseDuration = 30 * time.Millisecond
	coredns.RecoveryLeaseRenewInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		coredns.RecoveryLeaseDuration = origDuration
		coredns.RecoveryLeaseRenewInterval = origInterval
	})

	allocatedIP := "10.152.183.211"
	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"name":      "coredns",
				"clusterIP": allocatedIP,
			},
		},
	}

	// Even if the crashed peer's clock was 1 hour in the FUTURE, local monotonic observation
	// steals the unchanged lease once RecoveryLeaseDuration elapses locally.
	futureSkewedTime := metav1.NewMicroTime(time.Now().Add(1 * time.Hour))
	abandonedLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "ck-dns-recovery",
			Namespace:       "kube-system",
			ResourceVersion: "1",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("crashed-cp-node"),
			LeaseDurationSeconds: ptr.To(int32(60)),
			AcquireTime:          &futureSkewedTime,
			RenewTime:            &futureSkewedTime,
		},
	}
	recreatedService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: allocatedIP,
		},
	}
	clientset := fake.NewSimpleClientset(abandonedLease)
	uninstalled := false
	clientset.PrependReactor("get", "services", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		if !uninstalled {
			return true, nil, apierrors.NewNotFound(corev1.Resource("services"), "coredns")
		}
		return true, recreatedService, nil
	})

	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient: &slowUninstallMock{
				Mock: helmM,
				onDelete: func(ctx context.Context) error {
					uninstalled = true
					return nil
				},
			},
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}

	// 1. Initial observation records baseline without trusting peer wall clock.
	_, _, err := coredns.ApplyDNS(
		t.Context(),
		snapM,
		types.DNS{Enabled: ptr.To(true)},
		types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
		nil,
	)
	g.Expect(err).To(HaveOccurred())
	g.Expect(helmM.ApplyCalledWith).To(BeEmpty())

	// 2. Wait past local monotonic lease duration; unchanged lease is now stolen and recovered.
	time.Sleep(40 * time.Millisecond)
	status, ip, err := coredns.ApplyDNS(
		t.Context(),
		snapM,
		types.DNS{Enabled: ptr.To(true)},
		types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
		nil,
	)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal(allocatedIP))
	g.Expect(status.Enabled).To(BeTrue())
	g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))
	g.Expect(helmM.ApplyCalledWith[0].State).To(Equal(helm.StateDeleted))
	g.Expect(helmM.ApplyCalledWith[1].State).To(Equal(helm.StatePresent))

	// Lease must be cleaned up on return
	_, getErr := clientset.CoordinationV1().Leases("kube-system").Get(t.Context(), "ck-dns-recovery", metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue())
}

func TestApplyDNS_ClockSkewedPeerLease_UsesLocalMonotonicObservation(t *testing.T) {
	g := NewWithT(t)
	coredns.ResetObservedRecoveryLeases()
	t.Cleanup(coredns.ResetObservedRecoveryLeases)

	origDuration := coredns.RecoveryLeaseDuration
	origInterval := coredns.RecoveryLeaseRenewInterval
	coredns.RecoveryLeaseDuration = 40 * time.Millisecond
	coredns.RecoveryLeaseRenewInterval = 12 * time.Millisecond
	t.Cleanup(func() {
		coredns.RecoveryLeaseDuration = origDuration
		coredns.RecoveryLeaseRenewInterval = origInterval
	})

	allocatedIP := "10.152.183.211"
	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"name":      "coredns",
				"clusterIP": allocatedIP,
			},
		},
	}

	// Peer's clock is 70s behind this node's wall clock: by raw wall-clock comparison,
	// RenewTime (-70s) + 60s already looks expired even though the peer is actively renewing!
	skewedTime := metav1.NewMicroTime(time.Now().Add(-70 * time.Second))
	skewedLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "ck-dns-recovery",
			Namespace:       "kube-system",
			ResourceVersion: "1",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("clock-skewed-peer-node"),
			LeaseDurationSeconds: ptr.To(int32(60)),
			AcquireTime:          &skewedTime,
			RenewTime:            &skewedTime,
		},
	}
	clientset := fake.NewSimpleClientset(skewedLease)
	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmM,
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}

	// 1. First observation: must NOT steal the lease based on cross-node wall-clock comparison!
	status, _, err := coredns.ApplyDNS(
		t.Context(),
		snapM,
		types.DNS{Enabled: ptr.To(true)},
		types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
		nil,
	)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("recovery is already in progress"))
	g.Expect(status.Enabled).To(BeFalse())
	g.Expect(helmM.ApplyCalledWith).To(BeEmpty(), "must not steal clock-skewed peer lease on first local observation")

	// 2. Peer renews (still 69s behind our wall clock!), resetting our local observation timer.
	time.Sleep(25 * time.Millisecond)
	renewedSkewedTime := metav1.NewMicroTime(time.Now().Add(-69 * time.Second))
	skewedLease.Spec.RenewTime = &renewedSkewedTime
	_, err = clientset.CoordinationV1().Leases("kube-system").Update(t.Context(), skewedLease, metav1.UpdateOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	time.Sleep(25 * time.Millisecond) // 50ms total (> 40ms duration), but only 25ms since peer's renewal!
	_, _, err = coredns.ApplyDNS(
		t.Context(),
		snapM,
		types.DNS{Enabled: ptr.To(true)},
		types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
		nil,
	)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("recovery is already in progress"))
	g.Expect(helmM.ApplyCalledWith).To(BeEmpty(), "must not steal lease when peer renewed within local lease duration")
}

func TestApplyDNS_NoDeployedRelease_ActivePeerLease_AbortsWithoutInstall(t *testing.T) {
	g := NewWithT(t)

	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: false,
	}

	now := metav1.NewMicroTime(time.Now())
	activeLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ck-dns-recovery",
			Namespace: "kube-system",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("peer-cp-node-between-uninstall-and-install"),
			LeaseDurationSeconds: ptr.To(int32(60)),
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}
	clientset := fake.NewSimpleClientset(activeLease)
	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmM,
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}

	status, ip, err := coredns.ApplyDNS(
		t.Context(),
		snapM,
		types.DNS{Enabled: ptr.To(true)},
		types.Kubelet{},
		nil,
	)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("recovery is already in progress"))
	g.Expect(ip).To(BeEmpty())
	g.Expect(status.Enabled).To(BeFalse())
	g.Expect(helmM.ApplyCalledWith).To(BeEmpty(), "must not start competing install while peer holds active lease")
}

func TestApplyDNS_RecoveryLeaseRenewalAndLostOwnership(t *testing.T) {
	origInterval := coredns.RecoveryLeaseRenewInterval
	coredns.RecoveryLeaseRenewInterval = 10 * time.Millisecond
	t.Cleanup(func() { coredns.RecoveryLeaseRenewInterval = origInterval })

	t.Run("RenewsLeaseDuringSlowRecovery", func(t *testing.T) {
		g := NewWithT(t)
		allocatedIP := "10.152.183.211"

		recreatedService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"},
			Spec:       corev1.ServiceSpec{ClusterIP: allocatedIP},
		}
		clientset := fake.NewSimpleClientset()
		leaseUpdates := 0
		clientset.PrependReactor("update", "leases", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			leaseUpdates++
			return false, nil, nil
		})

		uninstalled := false
		clientset.PrependReactor("get", "services", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			if !uninstalled {
				return true, nil, apierrors.NewNotFound(corev1.Resource("services"), "coredns")
			}
			return true, recreatedService, nil
		})

		helmM := &helmmock.Mock{
			GetDeployedConfigDeployed: true,
			GetDeployedConfigResult: map[string]any{
				"service": map[string]any{"name": "coredns", "clusterIP": allocatedIP},
			},
		}
		// Pause inside StateDeleted long enough for >= 2 lease renewals to fire.
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: &slowUninstallMock{
					Mock: helmM,
					onDelete: func(ctx context.Context) error {
						time.Sleep(35 * time.Millisecond)
						uninstalled = true
						return ctx.Err()
					},
				},
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		status, ip, err := coredns.ApplyDNS(
			t.Context(),
			snapM,
			types.DNS{Enabled: ptr.To(true)},
			types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
			nil,
		)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ip).To(Equal(allocatedIP))
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(leaseUpdates).To(BeNumerically(">=", 2))
	})

	t.Run("StopsBeforeInstallWhenLeaseOwnershipLost", func(t *testing.T) {
		g := NewWithT(t)
		allocatedIP := "10.152.183.211"

		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("update", "leases", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewConflict(coordinationv1.Resource("leases"), "ck-dns-recovery", errors.New("stolen by peer"))
		})

		helmM := &helmmock.Mock{
			GetDeployedConfigDeployed: true,
			GetDeployedConfigResult: map[string]any{
				"service": map[string]any{"name": "coredns", "clusterIP": allocatedIP},
			},
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: &slowUninstallMock{
					Mock: helmM,
					onDelete: func(ctx context.Context) error {
						select {
						case <-ctx.Done():
							return nil
						case <-time.After(200 * time.Millisecond):
							return errors.New("expected lease context cancellation on lost ownership")
						}
					},
				},
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		status, _, err := coredns.ApplyDNS(
			t.Context(),
			snapM,
			types.DNS{Enabled: ptr.To(true)},
			types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
			nil,
		)
		g.Expect(err).To(HaveOccurred())
		g.Expect(status.Enabled).To(BeFalse())
		// StateDeleted ran, lost lease ownership, and StatePresent MUST NOT have been called!
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))
		g.Expect(helmM.ApplyCalledWith[0].State).To(Equal(helm.StateDeleted))

		// Stolen lease must NOT have been deleted by the former holder's defer
		_, getErr := clientset.CoordinationV1().Leases("kube-system").Get(t.Context(), "ck-dns-recovery", metav1.GetOptions{})
		g.Expect(getErr).NotTo(HaveOccurred())
	})
}

func TestApplyDNS_SlowLeaseAcquisition(t *testing.T) {
	coredns.ResetObservedRecoveryLeases()
	t.Cleanup(coredns.ResetObservedRecoveryLeases)

	origDuration := coredns.RecoveryLeaseDuration
	origInterval := coredns.RecoveryLeaseRenewInterval
	coredns.RecoveryLeaseDuration = 60 * time.Millisecond
	coredns.RecoveryLeaseRenewInterval = 18 * time.Millisecond
	t.Cleanup(func() {
		coredns.RecoveryLeaseDuration = origDuration
		coredns.RecoveryLeaseRenewInterval = origInterval
	})

	t.Run("RejectsNearlyExpiredAcquisitionBeforeHelmMutation", func(t *testing.T) {
		g := NewWithT(t)
		allocatedIP := "10.152.183.211"

		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("create", "leases", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			// Sleep past leaseDuration - safetyMargin (60ms - 10ms = 50ms) during Create.
			time.Sleep(55 * time.Millisecond)
			return false, nil, nil
		})

		helmM := &helmmock.Mock{
			GetDeployedConfigDeployed: true,
			GetDeployedConfigResult: map[string]any{
				"service": map[string]any{"name": "coredns", "clusterIP": allocatedIP},
			},
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient:       helmM,
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		status, _, err := coredns.ApplyDNS(
			t.Context(),
			snapM,
			types.DNS{Enabled: ptr.To(true)},
			types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
			nil,
		)
		g.Expect(err).To(HaveOccurred())
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(helmM.ApplyCalledWith).To(BeEmpty(), "must abort before Helm recovery when acquired lease is nearly expired")
	})

	t.Run("DerivesInitialRenewalFromAcquiredTimestampAndRenewsImmediately", func(t *testing.T) {
		g := NewWithT(t)
		allocatedIP := "10.152.183.211"

		recreatedService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"},
			Spec:       corev1.ServiceSpec{ClusterIP: allocatedIP},
		}
		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("create", "leases", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			// Simulate Create taking 25ms (> 18ms renew interval, < 50ms safety deadline).
			time.Sleep(25 * time.Millisecond)
			return false, nil, nil
		})

		var leaseUpdates atomic.Int32
		clientset.PrependReactor("update", "leases", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			leaseUpdates.Add(1)
			return false, nil, nil
		})

		uninstalled := false
		clientset.PrependReactor("get", "services", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			if !uninstalled {
				return true, nil, apierrors.NewNotFound(corev1.Resource("services"), "coredns")
			}
			return true, recreatedService, nil
		})

		helmM := &helmmock.Mock{
			GetDeployedConfigDeployed: true,
			GetDeployedConfigResult: map[string]any{
				"service": map[string]any{"name": "coredns", "clusterIP": allocatedIP},
			},
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: &slowUninstallMock{
					Mock: helmM,
					onDelete: func(ctx context.Context) error {
						uninstalled = true
						return ctx.Err()
					},
				},
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}

		status, ip, err := coredns.ApplyDNS(
			t.Context(),
			snapM,
			types.DNS{Enabled: ptr.To(true)},
			types.Kubelet{ClusterDNS: ptr.To(allocatedIP)},
			nil,
		)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ip).To(Equal(allocatedIP))
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(leaseUpdates.Load()).To(BeNumerically(">=", 1), "must renew synchronously before returning when Create exceeded interval")
	})
}

type slowUninstallMock struct {
	*helmmock.Mock
	onDelete func(ctx context.Context) error
}

func (s *slowUninstallMock) Apply(ctx context.Context, c helm.InstallableChart, desired helm.State, values map[string]any) (bool, error) {
	changed, err := s.Mock.Apply(ctx, c, desired, values)
	if err != nil {
		return changed, err
	}
	if desired == helm.StateDeleted && s.onDelete != nil {
		if hookErr := s.onDelete(ctx); hookErr != nil {
			return false, hookErr
		}
	}
	return changed, nil
}

func TestApplyDNS_SecondReconcilePassesEmptyClusterIPToAvoidRevision2(t *testing.T) {
	g := NewWithT(t)

	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"clusterIP": "",
			},
		},
	}
	clusterIp := "10.152.183.211"
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
	kubelet := types.Kubelet{
		ClusterDNS: ptr.To(clusterIp),
	}

	status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(str).To(Equal(clusterIp))
	g.Expect(status.Enabled).To(BeTrue())
	g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

	callArgs := helmM.ApplyCalledWith[0]
	svcVal, ok := callArgs.Values["service"].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(svcVal["clusterIP"]).To(Equal(""), "clusterIP should be reset to empty string to match oldConfig")
}

func TestApplyDNS_ExplicitClusterIP_PreservesIP(t *testing.T) {
	g := NewWithT(t)

	clusterIp := "10.152.183.53"
	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"clusterIP": clusterIp,
			},
		},
	}
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
	kubelet := types.Kubelet{
		ClusterDNS: ptr.To(clusterIp),
	}

	status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(str).To(Equal(clusterIp))
	g.Expect(status.Enabled).To(BeTrue())
	g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

	callArgs := helmM.ApplyCalledWith[0]
	svcVal, ok := callArgs.Values["service"].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(svcVal["clusterIP"]).To(Equal(clusterIp), "explicit clusterIP must be preserved")
}

func TestApplyDNS_OutOfBandServiceDeleted_UninstallsBeforeReinstall(t *testing.T) {
	g := NewWithT(t)

	clusterIp := "10.152.183.211"
	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"clusterIP": "",
			},
		},
	}
	// Service does not exist in K8s (out-of-band deleted)
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
	kubelet := types.Kubelet{
		ClusterDNS: ptr.To(clusterIp),
	}

	// Apply will attempt uninstall first, then reinstall. Post-apply GetServiceClusterIP fails in fake clientset:
	status, _, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)
	g.Expect(err).To(HaveOccurred())
	g.Expect(status.Enabled).To(BeFalse())

	// Must have called Apply twice: first StateDeleted, then StatePresent
	g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))
	g.Expect(helmM.ApplyCalledWith[0].State).To(Equal(helm.StateDeleted))
	g.Expect(helmM.ApplyCalledWith[1].State).To(Equal(helm.StatePresent))
	svcVal, ok := helmM.ApplyCalledWith[1].Values["service"].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(svcVal["clusterIP"]).To(Equal(clusterIp), "service must be recreated with stored clusterIP")
}

func TestApplyDNS_OutOfBandServiceDeleted_UnsetClusterDNS_UninstallsBeforeReinstall(t *testing.T) {
	g := NewWithT(t)

	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"clusterIP": "",
			},
		},
	}
	// Service does not exist in K8s (out-of-band deleted)
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
	kubelet := types.Kubelet{} // ClusterDNS == ""

	// Apply will attempt uninstall first, then reinstall. Post-apply GetServiceClusterIP fails in fake clientset:
	status, _, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)
	g.Expect(err).To(HaveOccurred())
	g.Expect(status.Enabled).To(BeFalse())

	// Must have called Apply twice: first StateDeleted, then StatePresent
	g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))
	g.Expect(helmM.ApplyCalledWith[0].State).To(Equal(helm.StateDeleted))
	g.Expect(helmM.ApplyCalledWith[1].State).To(Equal(helm.StatePresent))
	svcVal, ok := helmM.ApplyCalledWith[1].Values["service"].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(svcVal["clusterIP"]).To(Equal(""), "service must be recreated with empty clusterIP for dynamic allocation")
}

func TestApplyDNS_DriftedServiceIP_UninstallsBeforeReinstall(t *testing.T) {
	g := NewWithT(t)

	clusterIp := "10.152.183.211"
	helmM := &helmmock.Mock{
		GetDeployedConfigDeployed: true,
		GetDeployedConfigResult: map[string]any{
			"service": map[string]any{
				"clusterIP": "",
			},
		},
	}
	// Service exists in K8s with drifted IP (e.g. 10.152.183.99)
	driftedService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.152.183.99",
		},
	}
	clientset := fake.NewSimpleClientset(driftedService)
	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmM,
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}
	dns := types.DNS{
		Enabled: ptr.To(true),
	}
	kubelet := types.Kubelet{
		ClusterDNS: ptr.To(clusterIp),
	}

	// Post-apply will see driftedService again and return its IP
	status, ip, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ip).To(Equal("10.152.183.99"))
	g.Expect(status.Enabled).To(BeTrue())

	// Must have called Apply twice: first StateDeleted, then StatePresent
	g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))
	g.Expect(helmM.ApplyCalledWith[0].State).To(Equal(helm.StateDeleted))
	g.Expect(helmM.ApplyCalledWith[1].State).To(Equal(helm.StatePresent))
	svcVal, ok := helmM.ApplyCalledWith[1].Values["service"].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(svcVal["clusterIP"]).To(Equal(clusterIp), "service must be recreated with target clusterIP")
}

func TestApplyDNS_TransientK8sError_FailsFastWithoutApplying(t *testing.T) {
	g := NewWithT(t)

	clusterIp := "10.152.183.211"
	helmM := &helmmock.Mock{}
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("get", "services", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, apierrors.NewInternalError(errors.New("apiserver timeout"))
	})

	snapM := &snapmock.Snap{
		Mock: snapmock.Mock{
			HelmClient:       helmM,
			KubernetesClient: &kubernetes.Client{Interface: clientset},
		},
	}
	dns := types.DNS{
		Enabled: ptr.To(true),
	}
	kubelet := types.Kubelet{
		ClusterDNS: ptr.To(clusterIp),
	}

	status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to retrieve the coredns service"))
	g.Expect(str).To(BeEmpty())
	g.Expect(status.Enabled).To(BeFalse())
	g.Expect(helmM.ApplyCalledWith).To(BeEmpty(), "m.Apply must not be called when k8s client returns transient error")
}

func TestApplyDNS_GetDeployedConfigError_FailsFastWithoutApplying(t *testing.T) {
	g := NewWithT(t)

	clusterIp := "10.152.183.211"
	helmM := &helmmock.Mock{
		GetDeployedConfigErr: errors.New("helm storage failure"),
	}
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
	kubelet := types.Kubelet{
		ClusterDNS: ptr.To(clusterIp),
	}

	status, str, err := coredns.ApplyDNS(context.Background(), snapM, dns, kubelet, nil)

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to get deployed release config"))
	g.Expect(str).To(BeEmpty())
	g.Expect(status.Enabled).To(BeFalse())
	g.Expect(helmM.ApplyCalledWith).To(BeEmpty(), "m.Apply must not be called when GetDeployedConfig returns error")
}
