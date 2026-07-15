package types_test

import (
	"testing"

	metallbAnnotations "github.com/canonical/k8s-snap-api/v2/api/annotations/metallb"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestValidateCIDR(t *testing.T) {
	for _, tc := range []struct {
		cidr         string
		expectPodErr bool
		expectSvcErr bool
	}{
		{cidr: "192.168.0.0/16"},
		{cidr: "2001:0db8::/108"},
		{cidr: "10.2.0.0/16,2001:0db8::/108"},
		{cidr: "", expectPodErr: true, expectSvcErr: true},
		{cidr: "bananas", expectPodErr: true, expectSvcErr: true},
		{cidr: "fd01::/108,fd02::/108,fd03::/108", expectPodErr: true, expectSvcErr: true},
		{cidr: "10.1.0.0/32", expectPodErr: true, expectSvcErr: true},
		{cidr: "2001:0db8::/32", expectSvcErr: true},
	} {
		t.Run(tc.cidr, func(t *testing.T) {
			t.Run("Pod", func(t *testing.T) {
				g := NewWithT(t)
				config := types.ClusterConfig{
					Network: types.Network{
						PodCIDR:     utils.Pointer(tc.cidr),
						ServiceCIDR: utils.Pointer("10.1.0.0/16"),
					},
				}
				err := config.Validate()
				if tc.expectPodErr {
					g.Expect(err).To(HaveOccurred())
				} else {
					g.Expect(err).To(Not(HaveOccurred()))
				}
			})
			t.Run("Service", func(t *testing.T) {
				g := NewWithT(t)
				config := types.ClusterConfig{
					Network: types.Network{
						PodCIDR:     utils.Pointer("10.1.0.0/16"),
						ServiceCIDR: utils.Pointer(tc.cidr),
					},
				}
				err := config.Validate()
				if tc.expectSvcErr {
					g.Expect(err).To(HaveOccurred())
				} else {
					g.Expect(err).To(Not(HaveOccurred()))
				}
			})
		})
	}
}

func TestValidateExternalServers(t *testing.T) {
	for _, tc := range []struct {
		name          string
		clusterConfig types.ClusterConfig
		expectErr     bool
	}{
		{name: "Empty", clusterConfig: types.ClusterConfig{Datastore: types.Datastore{ExternalServers: nil}}},
		{
			name: "HostPort", clusterConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					ExternalServers: utils.Pointer([]string{"localhost:123"}),
				},
			},
		},
		{
			name: "FQDN", clusterConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					ExternalServers: utils.Pointer([]string{"172.22.1.1.ec2.internal"}),
				},
			},
		},
		{
			name: "IPv4", clusterConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					ExternalServers: utils.Pointer([]string{"10.11.12.13"}),
				},
			},
		},
		{
			name: "IPv6", clusterConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					ExternalServers: utils.Pointer([]string{"http://[2001:0db8:85a3:0000:0000:8a2e:0370:7334]"}),
				},
			},
		},
		{
			name: "ValidMultiple", clusterConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					ExternalServers: utils.Pointer([]string{"https://localhost:123", "10.11.12.13"}),
				},
			},
		},
		{
			name: "InvalidSingle", clusterConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					ExternalServers: utils.Pointer([]string{"invalid_address:1:2"}),
				},
			},
			expectErr: true,
		},
		{
			name: "InvalidMultiple", clusterConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					ExternalServers: utils.Pointer([]string{"localhost:123", "invalid_address:1:2"}),
				},
			},
			expectErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			tc.clusterConfig.SetDefaults()

			err := tc.clusterConfig.Validate()
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(Not(HaveOccurred()))
			}
		})
	}
}

func TestValidateKubeProxyEnabled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		network   types.Network
		expectErr bool
	}{
		{
			name: "NetworkEnabled/KubeProxyEnabledExplicitlyTrue",
			network: types.Network{
				Enabled:          utils.Pointer(true),
				PodCIDR:          utils.Pointer("10.1.0.0/16"),
				ServiceCIDR:      utils.Pointer("10.2.0.0/16"),
				KubeProxyEnabled: utils.Pointer(true),
			},
			expectErr: true,
		},
		{
			name: "NetworkEnabled/KubeProxyEnabledExplicitlyFalse",
			network: types.Network{
				Enabled:          utils.Pointer(true),
				PodCIDR:          utils.Pointer("10.1.0.0/16"),
				ServiceCIDR:      utils.Pointer("10.2.0.0/16"),
				KubeProxyEnabled: utils.Pointer(false),
			},
			expectErr: false,
		},
		{
			name: "NetworkEnabled/KubeProxyEnabledNotSet",
			network: types.Network{
				Enabled:          utils.Pointer(true),
				PodCIDR:          utils.Pointer("10.1.0.0/16"),
				ServiceCIDR:      utils.Pointer("10.2.0.0/16"),
				KubeProxyEnabled: nil,
			},
			expectErr: false,
		},
		{
			name: "NetworkDisabled/KubeProxyEnabledExplicitlyTrue",
			network: types.Network{
				Enabled:          utils.Pointer(false),
				PodCIDR:          utils.Pointer("10.1.0.0/16"),
				ServiceCIDR:      utils.Pointer("10.2.0.0/16"),
				KubeProxyEnabled: utils.Pointer(true),
			},
			expectErr: false,
		},
		{
			name: "NetworkDisabled/KubeProxyEnabledNotSet",
			network: types.Network{
				Enabled:          utils.Pointer(false),
				PodCIDR:          utils.Pointer("10.1.0.0/16"),
				ServiceCIDR:      utils.Pointer("10.2.0.0/16"),
				KubeProxyEnabled: nil,
			},
			expectErr: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			config := types.ClusterConfig{Network: tc.network}

			err := config.Validate()
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestValidateBGPMode(t *testing.T) {
	bgpLB := func(mode bool, localASN int, peerAddr string, peerASN int) types.LoadBalancer {
		return types.LoadBalancer{
			BGPMode:        utils.Pointer(mode),
			BGPLocalASN:    utils.Pointer(localASN),
			BGPPeerAddress: utils.Pointer(peerAddr),
			BGPPeerASN:     utils.Pointer(peerASN),
		}
	}

	for _, tc := range []struct {
		name        string
		lb          types.LoadBalancer
		annotations types.Annotations
		expectErr   bool
	}{
		{
			name:      "BGPDisabled/NoFieldsRequired",
			lb:        types.LoadBalancer{BGPMode: utils.Pointer(false)},
			expectErr: false,
		},
		{
			name:      "BGPEnabled/MissingLocalASN",
			lb:        bgpLB(true, 0, "10.0.0.1", 65001),
			expectErr: true,
		},
		{
			name:      "BGPEnabled/MissingPeerAddress",
			lb:        bgpLB(true, 65000, "", 65001),
			expectErr: true,
		},
		{
			name:      "BGPEnabled/MissingPeerASN",
			lb:        bgpLB(true, 65000, "10.0.0.1", 0),
			expectErr: true,
		},
		{
			name:      "BGPEnabled/AllTypedFieldsSet",
			lb:        bgpLB(true, 65000, "10.0.0.1", 65001),
			expectErr: false,
		},
		{
			// bgp-peers annotation present: typed peer fields are optional.
			name: "BGPEnabled/AnnotationPeers/TypedPeerFieldsOmitted",
			lb: types.LoadBalancer{
				BGPMode:     utils.Pointer(true),
				BGPLocalASN: utils.Pointer(65000),
			},
			annotations: types.Annotations{
				metallbAnnotations.AnnotationBGPPeers: "- peerAddress: 10.0.0.1\n  peerASN: 65001\n",
			},
			expectErr: false,
		},
		{
			// bgp-local-asn is always required even when annotation is present.
			name: "BGPEnabled/AnnotationPeers/MissingLocalASN",
			lb: types.LoadBalancer{
				BGPMode: utils.Pointer(true),
			},
			annotations: types.Annotations{
				metallbAnnotations.AnnotationBGPPeers: "- peerAddress: 10.0.0.1\n  peerASN: 65001\n",
			},
			expectErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			config := types.ClusterConfig{
				Network: types.Network{
					PodCIDR:     utils.Pointer("10.1.0.0/16"),
					ServiceCIDR: utils.Pointer("10.2.0.0/16"),
				},
				LoadBalancer: tc.lb,
				Annotations:  tc.annotations,
			}
			if tc.expectErr {
				g.Expect(config.Validate()).To(HaveOccurred())
			} else {
				g.Expect(config.Validate()).ToNot(HaveOccurred())
			}
		})
	}
}
