package metrics_server_test

import (
	"context"
	"errors"
	"testing"

	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations/metrics-server"
	"github.com/canonical/k8sd/pkg/client/helm"
	helmmock "github.com/canonical/k8sd/pkg/client/helm/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	metrics_server "github.com/canonical/k8sd/pkg/k8sd/features/metrics-server"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestApplyMetricsServer(t *testing.T) {
	helmErr := errors.New("failed to apply")
	for _, tc := range []struct {
		name        string
		config      types.MetricsServer
		expectState helm.State
		helmError   error
	}{
		{
			name: "EnableWithoutHelmError",
			config: types.MetricsServer{
				Enabled: utils.Pointer(true),
			},
			expectState: helm.StatePresent,
			helmError:   nil,
		},
		{
			name: "DisableWithoutHelmError",
			config: types.MetricsServer{
				Enabled: utils.Pointer(false),
			},
			expectState: helm.StateDeleted,
			helmError:   nil,
		},
		{
			name: "EnableWithHelmError",
			config: types.MetricsServer{
				Enabled: utils.Pointer(true),
			},
			expectState: helm.StatePresent,
			helmError:   helmErr,
		},
		{
			name: "DisableWithHelmError",
			config: types.MetricsServer{
				Enabled: utils.Pointer(false),
			},
			expectState: helm.StateDeleted,
			helmError:   helmErr,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			h := &helmmock.Mock{
				ApplyErr: tc.helmError,
			}
			s := &snapmock.Snap{
				Mock: snapmock.Mock{
					HelmClient: h,
				},
			}

			status, err := metrics_server.ApplyMetricsServer(context.Background(), s, tc.config, nil)
			if tc.helmError == nil {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
			}

			g.Expect(h.ApplyCalledWith).To(ConsistOf(SatisfyAll(
				HaveField("Chart.Name", Equal("metrics-server")),
				HaveField("Chart.Namespace", Equal("kube-system")),
				HaveField("State", Equal(tc.expectState)),
			)))
			switch {
			case errors.Is(tc.helmError, helmErr):
				g.Expect(status.Message).To(ContainSubstring(helmErr.Error()))
			case tc.config.GetEnabled():
				g.Expect(status.Message).To(Equal("enabled"))
			default:
				g.Expect(status.Message).To(Equal("disabled"))
			}
		})
	}

	t.Run("Annotations", func(t *testing.T) {
		g := NewWithT(t)
		h := &helmmock.Mock{}
		s := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: h,
			},
		}

		cfg := types.MetricsServer{
			Enabled: utils.Pointer(true),
		}
		annotations := types.Annotations{
			apiv1_annotations.AnnotationImageRepo: "custom-image",
			apiv1_annotations.AnnotationImageTag:  "custom-tag",
		}

		status, err := metrics_server.ApplyMetricsServer(context.Background(), s, cfg, annotations)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(h.ApplyCalledWith).To(ConsistOf(HaveField("Values", HaveKeyWithValue("image", SatisfyAll(
			HaveKeyWithValue("repository", "custom-image"),
			HaveKeyWithValue("tag", "custom-tag"),
		)))))
		g.Expect(status.Message).To(Equal("enabled"))
	})
}

func TestConfigMapOverrides(t *testing.T) {
	cfg := types.MetricsServer{Enabled: utils.Pointer(true)}

	newSnap := func(objects ...k8sruntime.Object) *snapmock.Snap {
		clientset := fake.NewSimpleClientset(objects...)
		return &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient:       &helmmock.Mock{},
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}
	}

	configMap := func(valuesYAML string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-metrics-server-values", Namespace: "kube-system"},
			Data:       map[string]string{"values": valuesYAML},
		}
	}

	t.Run("NoConfigMap", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap()

		status, err := metrics_server.ApplyMetricsServer(context.Background(), snapM, cfg, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(Equal("enabled"))
	})

	t.Run("OverrideScalarValue", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("securityContext:\n  readOnlyRootFilesystem: true\n"))

		_, err := metrics_server.ApplyMetricsServer(context.Background(), snapM, cfg, nil)

		g.Expect(err).NotTo(HaveOccurred())
		helmValues := snapM.Mock.HelmClient.(*helmmock.Mock).ApplyCalledWith[0].Values
		sc := helmValues["securityContext"].(map[string]any)
		g.Expect(sc["readOnlyRootFilesystem"]).To(BeTrue())
	})

	t.Run("InvalidYAMLFallsBackToDefaults", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("this: is: not: valid: yaml: :::"))

		status, err := metrics_server.ApplyMetricsServer(context.Background(), snapM, cfg, nil)

		// ApplyMetricsServer should not fail — it uses defaults and surfaces the warning in status.
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(ContainSubstring("warning:"))
		g.Expect(status.Message).To(ContainSubstring("failed to parse values"))
	})

	t.Run("ValidOverrideHasNoWarningInStatus", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("securityContext:\n  readOnlyRootFilesystem: true\n"))

		status, err := metrics_server.ApplyMetricsServer(context.Background(), snapM, cfg, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(Equal("enabled"))
	})
}
