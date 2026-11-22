package app

import (
	"context"
	"sync/atomic"
	"testing"

	upgradesv1alpha "github.com/canonical/k8s-snap-api/v2/api/v1alpha"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	upgradepkg "github.com/canonical/k8sd/pkg/upgrade"
	"github.com/canonical/k8sd/pkg/version"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestPerformPostUpgrade_ExistingUpgradeCR(t *testing.T) {
	thisVer := versionutil.MustParse("1.36.0")
	versionData := version.Info{Revision: "100", KubernetesVersion: thisVer}
	upgradeName := upgradepkg.GetName(versionData)

	t.Run("RevisitCompleted", func(t *testing.T) {
		g := NewWithT(t)
		scheme, err := kubernetes.NewScheme()
		g.Expect(err).NotTo(HaveOccurred())

		ctx := context.Background()
		existing := &upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name: upgradeName,
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase:         upgradesv1alpha.UpgradePhaseCompleted,
				Strategy:      upgradesv1alpha.UpgradeStrategyRollingUpgrade,
				UpgradedNodes: []string{"node1", "node2"},
			},
		}

		fakeClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).WithStatusSubresource(existing).Build()
		k8sClient := &kubernetes.Client{Client: fakeClient}
		mockSnap := &snapmock.Snap{
			Mock: snapmock.Mock{
				Revision:              "100",
				NodeKubernetesVersion: thisVer,
				KubernetesClient:      k8sClient,
			},
		}

		app := &App{snap: mockSnap}
		state := &mockStateForUpgradeTest{name: "node1"}

		err = app.performPostUpgrade(ctx, state)
		g.Expect(err).NotTo(HaveOccurred())

		var updated upgradesv1alpha.Upgrade
		err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: upgradeName}, &updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseNodeUpgrade))
		g.Expect(updated.Status.Strategy).To(Equal(upgradesv1alpha.UpgradeStrategyInPlace))
		g.Expect(updated.Status.UpgradedNodes).To(Equal([]string{"node1"}))
	})

	t.Run("ResetFromFailed", func(t *testing.T) {
		g := NewWithT(t)
		scheme, err := kubernetes.NewScheme()
		g.Expect(err).NotTo(HaveOccurred())

		ctx := context.Background()
		existing := &upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name: upgradeName,
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase:         upgradesv1alpha.UpgradePhaseFailed,
				Strategy:      upgradesv1alpha.UpgradeStrategyRollingDowngrade,
				UpgradedNodes: []string{"failed-node"},
			},
		}

		fakeClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).WithStatusSubresource(existing).Build()
		k8sClient := &kubernetes.Client{Client: fakeClient}
		mockSnap := &snapmock.Snap{
			Mock: snapmock.Mock{
				Revision:              "100",
				NodeKubernetesVersion: thisVer,
				KubernetesClient:      k8sClient,
			},
		}

		app := &App{snap: mockSnap}
		state := &mockStateForUpgradeTest{name: "node1"}

		err = app.performPostUpgrade(ctx, state)
		g.Expect(err).NotTo(HaveOccurred())

		var updated upgradesv1alpha.Upgrade
		err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: upgradeName}, &updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseNodeUpgrade))
		g.Expect(updated.Status.Strategy).To(Equal(upgradesv1alpha.UpgradeStrategyInPlace))
		g.Expect(updated.Status.UpgradedNodes).To(Equal([]string{"node1"}))
	})
}

func TestPerformPostUpgrade_ConcurrentInProgress(t *testing.T) {
	g := NewWithT(t)
	scheme, err := kubernetes.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	thisVer := versionutil.MustParse("1.36.0")
	versionData := version.Info{Revision: "100", KubernetesVersion: thisVer}
	upgradeName := upgradepkg.GetName(versionData)

	existing := &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: upgradeName,
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase:         upgradesv1alpha.UpgradePhaseNodeUpgrade,
			Strategy:      upgradesv1alpha.UpgradeStrategyInPlace,
			UpgradedNodes: []string{"node1"},
		},
	}

	fakeClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).WithStatusSubresource(existing).Build()
	k8sClient := &kubernetes.Client{Client: fakeClient}
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			Revision:              "100",
			NodeKubernetesVersion: thisVer,
			KubernetesClient:      k8sClient,
		},
	}

	app := &App{snap: mockSnap}
	state := &mockStateForUpgradeTest{name: "node2"}

	err = app.performPostUpgrade(ctx, state)
	g.Expect(err).NotTo(HaveOccurred())

	var updated upgradesv1alpha.Upgrade
	err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: upgradeName}, &updated)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(updated.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseNodeUpgrade))
	g.Expect(updated.Status.UpgradedNodes).To(ConsistOf("node1", "node2"))
}

func TestPerformPostUpgrade_ConcurrentConflict(t *testing.T) {
	g := NewWithT(t)
	scheme, err := kubernetes.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	thisVer := versionutil.MustParse("1.36.0")
	versionData := version.Info{Revision: "100", KubernetesVersion: thisVer}
	upgradeName := upgradepkg.GetName(versionData)

	existing := &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: upgradeName,
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase:         upgradesv1alpha.UpgradePhaseCompleted,
			Strategy:      upgradesv1alpha.UpgradeStrategyInPlace,
			UpgradedNodes: []string{"node-old"},
		},
	}

	var patchAttempts atomic.Int32
	interceptorFuncs := interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, client ctrlclient.Client, subResourceName string, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
			if subResourceName == "status" && patchAttempts.Add(1) == 1 {
				var current upgradesv1alpha.Upgrade
				if err := client.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), &current); err == nil {
					current.Status.Phase = upgradesv1alpha.UpgradePhaseFeatureUpgrade
					current.Status.UpgradedNodes = []string{"node1"}
					_ = client.Status().Update(ctx, &current)
				}
			}
			return client.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
	}

	fakeClient := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existing).
		WithStatusSubresource(existing).
		WithInterceptorFuncs(interceptorFuncs).
		Build()
	k8sClient := &kubernetes.Client{Client: fakeClient}
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			Revision:              "100",
			NodeKubernetesVersion: thisVer,
			KubernetesClient:      k8sClient,
		},
	}

	app := &App{snap: mockSnap}
	state := &mockStateForUpgradeTest{name: "node2"}

	err = app.performPostUpgrade(ctx, state)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(patchAttempts.Load()).To(BeNumerically(">=", 2))

	var updated upgradesv1alpha.Upgrade
	err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: upgradeName}, &updated)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(updated.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseFeatureUpgrade))
	g.Expect(updated.Status.UpgradedNodes).To(ConsistOf("node1", "node2"))
}
