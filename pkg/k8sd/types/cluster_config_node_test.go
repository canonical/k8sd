package types_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestClusterConfigConfigMap(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    types.ClusterConfig
		configmap map[string]string
	}{
		{
			name:      "Empty",
			configmap: map[string]string{},
		},
		{
			name: "KubeletOnly",
			configmap: map[string]string{
				"cluster-dns":    "10.152.183.10",
				"cluster-domain": "cluster.local",
				"cloud-provider": "external",
			},
			config: types.ClusterConfig{
				Kubelet: types.Kubelet{
					ClusterDNS:    utils.Pointer("10.152.183.10"),
					ClusterDomain: utils.Pointer("cluster.local"),
					CloudProvider: utils.Pointer("external"),
				},
			},
		},
		{
			name: "KubeletWithTaints",
			configmap: map[string]string{
				"control-plane-taints": `["node-role.kubernetes.io/control-plane=true:NoSchedule"]`,
			},
			config: types.ClusterConfig{
				Kubelet: types.Kubelet{
					ControlPlaneTaints: utils.Pointer([]string{"node-role.kubernetes.io/control-plane=true:NoSchedule"}),
				},
			},
		},
		{
			name: "NetworkOnly",
			configmap: map[string]string{
				"kube-proxy-free": "true",
			},
			config: types.ClusterConfig{
				Network: types.Network{
					KubeProxyFree: utils.Pointer(true),
				},
			},
		},
		{
			name: "Combined",
			configmap: map[string]string{
				"cluster-dns":          "10.152.183.10",
				"cluster-domain":       "cluster.local",
				"cloud-provider":       "external",
				"control-plane-taints": `["node-role.kubernetes.io/control-plane=true:NoSchedule"]`,
				"kube-proxy-free":      "true",
			},
			config: types.ClusterConfig{
				Kubelet: types.Kubelet{
					ClusterDNS:         utils.Pointer("10.152.183.10"),
					ClusterDomain:      utils.Pointer("cluster.local"),
					CloudProvider:      utils.Pointer("external"),
					ControlPlaneTaints: utils.Pointer([]string{"node-role.kubernetes.io/control-plane=true:NoSchedule"}),
				},
				Network: types.Network{
					KubeProxyFree: utils.Pointer(true),
				},
			},
		},
		{
			name: "EmptyKubeletValues",
			configmap: map[string]string{
				"cluster-dns":          "",
				"cluster-domain":       "",
				"cloud-provider":       "",
				"control-plane-taints": "[]",
			},
			config: types.ClusterConfig{
				Kubelet: types.Kubelet{
					ClusterDNS:         utils.Pointer(""),
					ClusterDomain:      utils.Pointer(""),
					CloudProvider:      utils.Pointer(""),
					ControlPlaneTaints: utils.Pointer([]string{}),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("ToConfigMap", func(t *testing.T) {
				g := NewWithT(t)

				cm, err := types.ClusterConfigToConfigMap(tc.config, nil)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(cm).To(Equal(tc.configmap))
			})

			t.Run("FromConfigMap", func(t *testing.T) {
				g := NewWithT(t)

				config, err := types.ConfigMapToClusterConfig(tc.configmap, nil)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(config.Kubelet).To(Equal(tc.config.Kubelet))
				g.Expect(config.Network.KubeProxyFree).To(Equal(tc.config.Network.KubeProxyFree))
			})
		})
	}
}

func TestClusterConfigSign(t *testing.T) {
	g := NewWithT(t)
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	g.Expect(err).To(Not(HaveOccurred()))

	config := types.ClusterConfig{
		Kubelet: types.Kubelet{
			CloudProvider: utils.Pointer("external"),
			ClusterDNS:    utils.Pointer("10.0.0.1"),
			ClusterDomain: utils.Pointer("cluster.local"),
		},
		Network: types.Network{
			KubeProxyFree: utils.Pointer(true),
		},
	}

	configmap, err := types.ClusterConfigToConfigMap(config, key)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(configmap).To(HaveKeyWithValue("k8sd-mac", Not(BeEmpty())))

	t.Run("NoSign", func(t *testing.T) {
		g := NewWithT(t)

		configmap, err := types.ClusterConfigToConfigMap(config, nil)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(configmap).To(Not(HaveKey("k8sd-mac")))
	})

	t.Run("SignAndVerify", func(t *testing.T) {
		g := NewWithT(t)

		parsed, err := types.ConfigMapToClusterConfig(configmap, &key.PublicKey)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(parsed.Kubelet).To(Equal(config.Kubelet))
		g.Expect(parsed.Network.GetKubeProxyFree()).To(BeTrue())
	})

	t.Run("DeterministicSignature", func(t *testing.T) {
		g := NewWithT(t)

		configmap2, err := types.ClusterConfigToConfigMap(config, key)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(configmap2).To(Equal(configmap))
	})

	t.Run("WrongKey", func(t *testing.T) {
		g := NewWithT(t)

		wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
		g.Expect(err).To(Not(HaveOccurred()))

		parsed, err := types.ConfigMapToClusterConfig(configmap, &wrongKey.PublicKey)
		g.Expect(parsed).To(BeZero())
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("BadSignature", func(t *testing.T) {
		for editKey := range configmap {
			t.Run(editKey, func(t *testing.T) {
				g := NewWithT(t)
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				g.Expect(err).To(Not(HaveOccurred()))

				c, err := types.ClusterConfigToConfigMap(config, key)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(c).To(HaveKeyWithValue("k8sd-mac", Not(BeEmpty())))

				t.Run("Manipulated", func(t *testing.T) {
					g := NewWithT(t)
					c[editKey] = "attack"

					parsed, err := types.ConfigMapToClusterConfig(c, &key.PublicKey)
					g.Expect(err).To(HaveOccurred())
					g.Expect(parsed).To(BeZero())
				})

				t.Run("Deleted", func(t *testing.T) {
					g := NewWithT(t)
					delete(c, editKey)

					parsed, err := types.ConfigMapToClusterConfig(c, &key.PublicKey)
					g.Expect(err).To(HaveOccurred())
					g.Expect(parsed).To(BeZero())
				})
			})
		}
	})
}

func TestClusterConfigRoundTrip(t *testing.T) {
	g := NewWithT(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("KubeletOnlyRoundTrip", func(t *testing.T) {
		g := NewWithT(t)
		config := types.ClusterConfig{
			Kubelet: types.Kubelet{
				CloudProvider: utils.Pointer("external"),
				ClusterDNS:    utils.Pointer("10.96.0.10"),
				ClusterDomain: utils.Pointer("cluster.local"),
			},
		}

		cm, err := types.ClusterConfigToConfigMap(config, key)
		g.Expect(err).To(Not(HaveOccurred()))

		parsed, err := types.ConfigMapToClusterConfig(cm, &key.PublicKey)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(parsed.Kubelet).To(Equal(config.Kubelet))
		g.Expect(parsed.Network.GetKubeProxyFree()).To(BeFalse())
	})

	t.Run("NetworkOnlyRoundTrip", func(t *testing.T) {
		g := NewWithT(t)
		config := types.ClusterConfig{
			Network: types.Network{
				KubeProxyFree: utils.Pointer(true),
			},
		}

		cm, err := types.ClusterConfigToConfigMap(config, key)
		g.Expect(err).To(Not(HaveOccurred()))

		parsed, err := types.ConfigMapToClusterConfig(cm, &key.PublicKey)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(parsed.Kubelet).To(Equal(types.Kubelet{}))
		g.Expect(parsed.Network.GetKubeProxyFree()).To(BeTrue())
	})

	t.Run("CombinedRoundTrip", func(t *testing.T) {
		g := NewWithT(t)
		config := types.ClusterConfig{
			Kubelet: types.Kubelet{
				CloudProvider:      utils.Pointer("external"),
				ClusterDNS:         utils.Pointer("10.96.0.10"),
				ClusterDomain:      utils.Pointer("cluster.local"),
				ControlPlaneTaints: utils.Pointer([]string{"node-role.kubernetes.io/control-plane:NoSchedule"}),
			},
			Network: types.Network{
				KubeProxyFree: utils.Pointer(true),
			},
		}

		cm, err := types.ClusterConfigToConfigMap(config, key)
		g.Expect(err).To(Not(HaveOccurred()))

		parsed, err := types.ConfigMapToClusterConfig(cm, &key.PublicKey)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(parsed.Kubelet).To(Equal(config.Kubelet))
		g.Expect(parsed.Network.GetKubeProxyFree()).To(BeTrue())
	})
}
