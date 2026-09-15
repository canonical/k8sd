package types_test

import (
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestClusterConfigFromUserFacingPatches(t *testing.T) {
	strategicMerge := "spec:\n  foo: bar\n"

	t.Run("Nil", func(t *testing.T) {
		g := NewWithT(t)
		config, err := types.ClusterConfigFromUserFacing(apiv2.UserFacingClusterConfig{})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(config.Network.Patches).To(BeNil())
	})

	t.Run("Valid", func(t *testing.T) {
		g := NewWithT(t)
		config, err := types.ClusterConfigFromUserFacing(apiv2.UserFacingClusterConfig{
			Network: apiv2.NetworkConfig{
				Patches: &[]apiv2.Patch{
					{
						Target:         apiv2.PatchTarget{Kind: "DaemonSet", Name: "cilium", Namespace: "kube-system"},
						StrategicMerge: utils.Pointer(strategicMerge),
					},
				},
			},
		})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(config.Network.GetPatches()).To(ConsistOf(types.Patch{
			Target:         types.PatchTarget{Kind: "DaemonSet", Name: "cilium", Namespace: "kube-system"},
			StrategicMerge: utils.Pointer(strategicMerge),
		}))
	})

	t.Run("InvalidMissingTarget", func(t *testing.T) {
		g := NewWithT(t)
		_, err := types.ClusterConfigFromUserFacing(apiv2.UserFacingClusterConfig{
			Network: apiv2.NetworkConfig{
				Patches: &[]apiv2.Patch{
					{StrategicMerge: utils.Pointer(strategicMerge)},
				},
			},
		})
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("InvalidMutuallyExclusive", func(t *testing.T) {
		g := NewWithT(t)
		_, err := types.ClusterConfigFromUserFacing(apiv2.UserFacingClusterConfig{
			Network: apiv2.NetworkConfig{
				Patches: &[]apiv2.Patch{
					{
						Target:         apiv2.PatchTarget{Kind: "DaemonSet", Name: "cilium"},
						StrategicMerge: utils.Pointer(strategicMerge),
						JSON6902:       utils.Pointer("[]"),
					},
				},
			},
		})
		g.Expect(err).To(HaveOccurred())
	})
}

func TestClusterConfigToUserFacingPatches(t *testing.T) {
	strategicMerge := "spec:\n  foo: bar\n"

	t.Run("Nil", func(t *testing.T) {
		g := NewWithT(t)
		config := types.ClusterConfig{}
		g.Expect(config.ToUserFacing().Network.Patches).To(BeNil())
	})

	t.Run("Valid", func(t *testing.T) {
		g := NewWithT(t)
		config := types.ClusterConfig{
			Network: types.Network{
				Patches: &[]types.Patch{
					{
						Target:         types.PatchTarget{Kind: "DaemonSet", Name: "cilium", Namespace: "kube-system"},
						StrategicMerge: utils.Pointer(strategicMerge),
					},
				},
			},
		}
		out := config.ToUserFacing()
		g.Expect(out.Network.GetPatches()).To(ConsistOf(apiv2.Patch{
			Target:         apiv2.PatchTarget{Kind: "DaemonSet", Name: "cilium", Namespace: "kube-system"},
			StrategicMerge: utils.Pointer(strategicMerge),
		}))
	})
}
