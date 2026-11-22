package kubernetes

import (
	"context"
	"testing"
	"time"

	upgradesv1alpha "github.com/canonical/k8s-snap-api/v2/api/v1alpha"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetInProgressUpgrade(t *testing.T) {
	scheme, err := NewScheme()
	if err != nil {
		t.Fatalf("failed to create scheme: %v", err)
	}

	ctx := context.Background()

	t.Run("FiltersFailedAndCompleted", func(t *testing.T) {
		g := NewWithT(t)
		now := time.Now()
		failed := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-failed",
				CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Hour)),
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase: upgradesv1alpha.UpgradePhaseFailed,
			},
		}
		completed := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-completed",
				CreationTimestamp: metav1.NewTime(now),
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase: upgradesv1alpha.UpgradePhaseCompleted,
			},
		}
		inProgress := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-in-progress",
				CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Hour)),
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase: upgradesv1alpha.UpgradePhaseNodeUpgrade,
			},
		}

		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&failed, &completed, &inProgress).Build(),
		}

		u, err := client.GetInProgressUpgrade(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).NotTo(BeNil())
		g.Expect(u.Name).To(Equal("upgrade-in-progress"))
	})

	t.Run("NoInProgressReturnsNil", func(t *testing.T) {
		g := NewWithT(t)
		completed := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name: "upgrade-completed",
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase: upgradesv1alpha.UpgradePhaseCompleted,
			},
		}

		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&completed).Build(),
		}

		u, err := client.GetInProgressUpgrade(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).To(BeNil())
	})
}

func TestPatchUpgradeStatus(t *testing.T) {
	g := NewWithT(t)
	scheme, err := NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	upgrade := &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: "upgrade-patch",
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase: upgradesv1alpha.UpgradePhaseNodeUpgrade,
		},
	}

	client := &Client{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(upgrade).WithStatusSubresource(upgrade).Build(),
	}

	err = client.Get(ctx, ctrlclient.ObjectKey{Name: upgrade.Name}, upgrade)
	g.Expect(err).NotTo(HaveOccurred())

	err = client.PatchUpgradeStatus(ctx, upgrade, upgradesv1alpha.UpgradeStatus{
		Phase: upgradesv1alpha.UpgradePhaseFeatureUpgrade,
	})
	g.Expect(err).NotTo(HaveOccurred())
	fetched := &upgradesv1alpha.Upgrade{}
	err = client.Get(ctx, ctrlclient.ObjectKey{Name: upgrade.Name}, fetched)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(fetched.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseFeatureUpgrade))
}

func TestCreateOrGetUpgrade(t *testing.T) {
	scheme, err := NewScheme()
	if err != nil {
		t.Fatalf("failed to create scheme: %v", err)
	}

	ctx := context.Background()

	t.Run("CreatesNewUpgrade", func(t *testing.T) {
		g := NewWithT(t)
		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		}

		upgrade := upgradesv1alpha.NewUpgrade("upgrade-new")
		err := client.CreateOrGetUpgrade(ctx, upgrade)
		g.Expect(err).NotTo(HaveOccurred())

		fetched := &upgradesv1alpha.Upgrade{}
		err = client.Get(ctx, ctrlclient.ObjectKey{Name: "upgrade-new"}, fetched)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(fetched.Name).To(Equal("upgrade-new"))
	})

	t.Run("GetsExistingUpgrade", func(t *testing.T) {
		g := NewWithT(t)
		existing := &upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name: "upgrade-existing",
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase:         upgradesv1alpha.UpgradePhaseCompleted,
				UpgradedNodes: []string{"node-1", "node-2"},
			},
		}
		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build(),
		}

		upgrade := upgradesv1alpha.NewUpgrade("upgrade-existing")
		err := client.CreateOrGetUpgrade(ctx, upgrade)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(upgrade.Name).To(Equal("upgrade-existing"))
		g.Expect(upgrade.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseCompleted))
		g.Expect(upgrade.Status.UpgradedNodes).To(ConsistOf("node-1", "node-2"))
	})
}
