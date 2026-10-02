package kubernetes

import (
	"context"
	"testing"
	"time"

	upgradesv1alpha "github.com/canonical/k8s-snap-api/v2/api/v1alpha"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetUpgrade(t *testing.T) {
	g := NewWithT(t)
	scheme, err := NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()

	t.Run("NoUpgrades", func(t *testing.T) {
		g := NewWithT(t)
		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		}

		u, err := client.GetUpgrade(ctx, nil)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).To(BeNil())
	})

	t.Run("SingleMatch", func(t *testing.T) {
		g := NewWithT(t)
		upgrade := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name: "upgrade-1",
			},
			Status: upgradesv1alpha.UpgradeStatus{
				Phase: upgradesv1alpha.UpgradePhaseNodeUpgrade,
			},
		}
		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&upgrade).Build(),
		}

		u, err := client.GetUpgrade(ctx, func(u upgradesv1alpha.Upgrade) bool {
			return u.Name == "upgrade-1"
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).NotTo(BeNil())
		g.Expect(u.Name).To(Equal("upgrade-1"))
	})

	t.Run("NilFilterFuncMatchesAll", func(t *testing.T) {
		g := NewWithT(t)
		upgrade := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name: "upgrade-1",
			},
		}
		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&upgrade).Build(),
		}

		u, err := client.GetUpgrade(ctx, nil)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).NotTo(BeNil())
		g.Expect(u.Name).To(Equal("upgrade-1"))
	})

	t.Run("MultipleMatchesSortedByCreationTimestamp", func(t *testing.T) {
		g := NewWithT(t)
		now := time.Now()
		u1 := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-older",
				CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			},
		}
		u2 := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-newest",
				CreationTimestamp: metav1.NewTime(now),
			},
		}
		u3 := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-middle",
				CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Hour)),
			},
		}

		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&u1, &u2, &u3).Build(),
		}

		u, err := client.GetUpgrade(ctx, nil)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).NotTo(BeNil())
		g.Expect(u.Name).To(Equal("upgrade-newest"))
	})

	t.Run("MultipleMatchesSameTimestampSortedByName", func(t *testing.T) {
		g := NewWithT(t)
		ts := metav1.NewTime(time.Now())
		uA := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-a",
				CreationTimestamp: ts,
			},
		}
		uB := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "upgrade-b",
				CreationTimestamp: ts,
			},
		}

		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&uA, &uB).Build(),
		}

		u, err := client.GetUpgrade(ctx, nil)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).NotTo(BeNil())
		g.Expect(u.Name).To(Equal("upgrade-b"))
	})

	t.Run("FilterExcludesAll", func(t *testing.T) {
		g := NewWithT(t)
		upgrade := upgradesv1alpha.Upgrade{
			ObjectMeta: metav1.ObjectMeta{
				Name: "upgrade-1",
			},
		}
		client := &Client{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(&upgrade).Build(),
		}

		u, err := client.GetUpgrade(ctx, func(u upgradesv1alpha.Upgrade) bool {
			return false
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(u).To(BeNil())
	})
}

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

	err = client.PatchUpgradeStatus(ctx, upgrade, upgradesv1alpha.UpgradeStatus{
		Phase: upgradesv1alpha.UpgradePhaseFeatureUpgrade,
	})
	g.Expect(err).NotTo(HaveOccurred())

	fetched, err := client.GetUpgrade(ctx, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(fetched).NotTo(BeNil())
	g.Expect(fetched.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseFeatureUpgrade))
}
