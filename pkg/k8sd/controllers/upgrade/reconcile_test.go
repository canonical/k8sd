package upgrade

import (
	"context"
	"testing"

	upgradesv1alpha "github.com/canonical/k8s-snap-api/v2/api/v1alpha"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAddToUpgradedNodes_PhaseMovedOn(t *testing.T) {
	g := NewWithT(t)
	scheme, err := kubernetes.NewScheme()
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	upgrade := &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-upgrade",
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase:         upgradesv1alpha.UpgradePhaseFeatureUpgrade,
			UpgradedNodes: []string{"node1"},
		},
	}

	fakeClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(upgrade).WithStatusSubresource(upgrade).Build()
	c := &Controller{
		client: fakeClient,
		logger: logr.Discard(),
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node2",
		},
	}

	err = c.addToUpgradedNodes(ctx, upgrade, node)
	g.Expect(err).NotTo(HaveOccurred())

	var current upgradesv1alpha.Upgrade
	err = fakeClient.Get(ctx, ctrlclient.ObjectKey{Name: "test-upgrade"}, &current)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(current.Status.UpgradedNodes).To(Equal([]string{"node1"}))
	g.Expect(current.Status.UpgradedNodes).NotTo(ContainElement("node2"))
}
