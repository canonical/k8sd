package localpv_test

import (
	"context"
	"errors"
	"testing"

	"github.com/canonical/k8sd/pkg/client/helm"
	helmmock "github.com/canonical/k8sd/pkg/client/helm/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/localpv"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
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
		cfg := types.LocalStorage{
			Enabled:       ptr.To(false),
			Default:       ptr.To(true),
			ReclaimPolicy: ptr.To("reclaim-policy"),
			LocalPath:     ptr.To("local-path"),
		}

		status, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).To(MatchError(applyErr))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Version).To(Equal(localpv.ImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(localpv.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StateDeleted))

		validateValues(g, callArgs.Values, cfg)
	})
	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		cfg := types.LocalStorage{
			Enabled:       ptr.To(false),
			Default:       ptr.To(true),
			ReclaimPolicy: ptr.To("reclaim-policy"),
			LocalPath:     ptr.To("local-path"),
		}

		status, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Version).To(Equal(localpv.ImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(localpv.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StateDeleted))

		validateValues(g, callArgs.Values, cfg)
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
		cfg := types.LocalStorage{
			Enabled:       ptr.To(true),
			Default:       ptr.To(true),
			ReclaimPolicy: ptr.To("reclaim-policy"),
			LocalPath:     ptr.To("local-path"),
		}

		status, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).To(MatchError(applyErr))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Version).To(Equal(localpv.ImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(localpv.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StatePresent))

		validateValues(g, callArgs.Values, cfg)
	})
	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		cfg := types.LocalStorage{
			Enabled:       ptr.To(true),
			Default:       ptr.To(true),
			ReclaimPolicy: ptr.To("reclaim-policy"),
			LocalPath:     ptr.To("local-path"),
		}

		status, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Version).To(Equal(localpv.ImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(localpv.Chart))
		g.Expect(callArgs.State).To(Equal(helm.StatePresent))

		validateValues(g, callArgs.Values, cfg)
	})
}

func validateValues(g Gomega, values map[string]any, cfg types.LocalStorage) {
	sc := values["storageClass"].(map[string]any)
	g.Expect(sc["isDefault"]).To(Equal(cfg.GetDefault()))
	g.Expect(sc["reclaimPolicy"]).To(Equal(cfg.GetReclaimPolicy()))
	g.Expect(sc["allowVolumeExpansion"]).To(BeFalse())

	storage := values["node"].(map[string]any)["storage"].(map[string]any)
	g.Expect(storage["path"]).To(Equal(cfg.GetLocalPath()))
}

func TestConfigMapOverrides(t *testing.T) {
	cfg := types.LocalStorage{
		Enabled:       ptr.To(true),
		Default:       ptr.To(true),
		ReclaimPolicy: ptr.To("Retain"),
		LocalPath:     ptr.To("/var/snap/k8s/common/rawfile-storage"),
	}

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
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-localpv-values", Namespace: "kube-system"},
			Data:       map[string]string{"values": valuesYAML},
		}
	}

	t.Run("NoConfigMap", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap()

		status, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).NotTo(ContainSubstring("warning"))
	})

	t.Run("OverrideScalarValue", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("storageClass:\n  reclaimPolicy: Delete\n"))

		_, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).NotTo(HaveOccurred())
		helmValues := snapM.Mock.HelmClient.(*helmmock.Mock).ApplyCalledWith[0].Values
		sc := helmValues["storageClass"].(map[string]any)
		g.Expect(sc["reclaimPolicy"]).To(Equal("Delete"))
	})

	t.Run("DeepMergePreservesUnrelatedKeys", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("storageClass:\n  reclaimPolicy: Delete\n"))

		_, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).NotTo(HaveOccurred())
		helmValues := snapM.Mock.HelmClient.(*helmmock.Mock).ApplyCalledWith[0].Values
		sc := helmValues["storageClass"].(map[string]any)
		// isDefault not in override — should keep default.
		g.Expect(sc["isDefault"]).To(Equal(cfg.GetDefault()))
	})

	t.Run("InvalidYAMLFallsBackToDefaults", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("this: is: not: valid: yaml: :::"))

		status, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		// ApplyLocalStorage should not fail — it uses defaults and surfaces the warning in status.
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(ContainSubstring("warning:"))
		g.Expect(status.Message).To(ContainSubstring("failed to parse configmap values"))
	})

	t.Run("ValidOverrideHasNoWarningInStatus", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("storageClass:\n  reclaimPolicy: Delete\n"))

		status, err := localpv.ApplyLocalStorage(context.Background(), snapM, cfg, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).NotTo(ContainSubstring("warning"))
	})
}
