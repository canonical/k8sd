package types_test

import (
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestClusterConfigFromBootstrapConfig(t *testing.T) {
	for _, tc := range []struct {
		name         string
		bootstrap    apiv2.BootstrapConfig
		expectConfig types.ClusterConfig
	}{
		{
			name: "Nil",
			expectConfig: types.ClusterConfig{
				APIServer: types.APIServer{
					AuthorizationMode: utils.Pointer("Node,RBAC"),
				},
				Datastore: types.Datastore{
					Type: utils.Pointer("etcd"),
				},
			},
		},
		{
			name: "DisableRBAC",
			bootstrap: apiv2.BootstrapConfig{
				DisableRBAC: utils.Pointer(true),
			},
			expectConfig: types.ClusterConfig{
				APIServer: types.APIServer{
					AuthorizationMode: utils.Pointer("AlwaysAllow"),
				},
				Datastore: types.Datastore{
					Type: utils.Pointer("etcd"),
				},
			},
		},
		{
			name: "EtcdDefault",
			bootstrap: apiv2.BootstrapConfig{
				DatastoreType: utils.Pointer(""),
			},
			expectConfig: types.ClusterConfig{
				APIServer: types.APIServer{
					AuthorizationMode: utils.Pointer("Node,RBAC"),
				},
				Datastore: types.Datastore{
					Type: utils.Pointer("etcd"),
				},
			},
		},
		{
			name: "ExternalDatastore",
			bootstrap: apiv2.BootstrapConfig{
				DatastoreType:       utils.Pointer("external"),
				DatastoreServers:    []string{"https://10.0.0.1:2379", "https://10.0.0.2:2379"},
				DatastoreCACert:     utils.Pointer("CA DATA"),
				DatastoreClientCert: utils.Pointer("CERT DATA"),
				DatastoreClientKey:  utils.Pointer("KEY DATA"),
			},
			expectConfig: types.ClusterConfig{
				APIServer: types.APIServer{
					AuthorizationMode: utils.Pointer("Node,RBAC"),
				},
				Datastore: types.Datastore{
					Type:               utils.Pointer("external"),
					ExternalServers:    utils.Pointer([]string{"https://10.0.0.1:2379", "https://10.0.0.2:2379"}),
					ExternalCACert:     utils.Pointer("CA DATA"),
					ExternalClientCert: utils.Pointer("CERT DATA"),
					ExternalClientKey:  utils.Pointer("KEY DATA"),
				},
			},
		},
		{
			name: "EtcdDatastore",
			bootstrap: apiv2.BootstrapConfig{
				DatastoreType: utils.Pointer("etcd"),
				EtcdPort:      utils.Pointer(12379),
				EtcdPeerPort:  utils.Pointer(12380),
			},
			expectConfig: types.ClusterConfig{
				APIServer: types.APIServer{
					AuthorizationMode: utils.Pointer("Node,RBAC"),
				},
				Datastore: types.Datastore{
					Type:         utils.Pointer("etcd"),
					EtcdPort:     utils.Pointer(12379),
					EtcdPeerPort: utils.Pointer(12380),
				},
			},
		},
		{
			name: "Full",
			bootstrap: apiv2.BootstrapConfig{
				ClusterConfig: apiv2.UserFacingClusterConfig{
					Annotations: map[string]string{
						"key": "value",
					},
					Network: apiv2.NetworkConfig{
						Enabled:       utils.Pointer(true),
						KubeProxyFree: utils.Pointer(true),
					},
					DNS: apiv2.DNSConfig{
						Enabled:       utils.Pointer(true),
						ClusterDomain: utils.Pointer("cluster.local"),
					},
					Ingress: apiv2.IngressConfig{
						Enabled: utils.Pointer(true),
					},
					LoadBalancer: apiv2.LoadBalancerConfig{
						Enabled: utils.Pointer(true),
						L2Mode:  utils.Pointer(true),
						CIDRs:   utils.Pointer([]string{"10.0.0.0/24", "10.1.0.10-10.1.0.20"}),
					},
					LocalStorage: apiv2.LocalStorageConfig{
						Enabled:   utils.Pointer(true),
						LocalPath: utils.Pointer("/storage/path"),
						Default:   utils.Pointer(false),
					},
					Gateway: apiv2.GatewayConfig{
						Enabled: utils.Pointer(true),
					},
					MetricsServer: apiv2.MetricsServerConfig{
						Enabled: utils.Pointer(true),
					},
					CloudProvider: utils.Pointer("external"),
				},
				PodCIDR:       utils.Pointer("10.100.0.0/16"),
				ServiceCIDR:   utils.Pointer("10.200.0.0/16"),
				DisableRBAC:   utils.Pointer(false),
				SecurePort:    utils.Pointer(6443),
				EtcdPort:      utils.Pointer(12379),
				EtcdPeerPort:  utils.Pointer(12380),
				DatastoreType: utils.Pointer("etcd"),
				ExtraSANs:     []string{"custom.kubernetes"},
			},
			expectConfig: types.ClusterConfig{
				Datastore: types.Datastore{
					Type:         utils.Pointer("etcd"),
					EtcdPort:     utils.Pointer(12379),
					EtcdPeerPort: utils.Pointer(12380),
				},
				APIServer: types.APIServer{
					SecurePort:        utils.Pointer(6443),
					AuthorizationMode: utils.Pointer("Node,RBAC"),
				},
				Kubelet: types.Kubelet{
					ClusterDomain: utils.Pointer("cluster.local"),
					CloudProvider: utils.Pointer("external"),
				},
				Network: types.Network{
					Enabled:       utils.Pointer(true),
					PodCIDR:       utils.Pointer("10.100.0.0/16"),
					ServiceCIDR:   utils.Pointer("10.200.0.0/16"),
					KubeProxyFree: utils.Pointer(true),
				},
				DNS: types.DNS{
					Enabled: utils.Pointer(true),
				},
				Ingress: types.Ingress{
					Enabled: utils.Pointer(true),
				},
				LoadBalancer: types.LoadBalancer{
					Enabled:  utils.Pointer(true),
					L2Mode:   utils.Pointer(true),
					CIDRs:    utils.Pointer([]string{"10.0.0.0/24"}),
					IPRanges: utils.Pointer([]types.LoadBalancer_IPRange{{Start: "10.1.0.10", Stop: "10.1.0.20"}}),
				},
				LocalStorage: types.LocalStorage{
					Enabled:   utils.Pointer(true),
					LocalPath: utils.Pointer("/storage/path"),
					Default:   utils.Pointer(false),
				},
				Gateway: types.Gateway{
					Enabled: utils.Pointer(true),
				},
				MetricsServer: types.MetricsServer{
					Enabled: utils.Pointer(true),
				},
				Annotations: types.Annotations{
					"key": "value",
				},
			},
		},
		{
			name: "ControlPlainTaints",
			bootstrap: apiv2.BootstrapConfig{
				ControlPlaneTaints: []string{"node-role.kubernetes.io/control-plane:NoSchedule"},
			},
			expectConfig: types.ClusterConfig{
				APIServer: types.APIServer{
					AuthorizationMode: utils.Pointer("Node,RBAC"),
				},
				Datastore: types.Datastore{
					Type: utils.Pointer("etcd"),
				},
				Kubelet: types.Kubelet{
					ControlPlaneTaints: utils.Pointer([]string{"node-role.kubernetes.io/control-plane:NoSchedule"}),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			config, err := types.ClusterConfigFromBootstrapConfig(tc.bootstrap)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(config).To(Equal(tc.expectConfig))
		})
	}

	t.Run("Invalid", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			bootstrap apiv2.BootstrapConfig
		}{
			{
				name: "ExternalWithoutServers",
				bootstrap: apiv2.BootstrapConfig{
					DatastoreType: utils.Pointer("external"),
				},
			},
			{
				name: "UnsupportedDatastore",
				bootstrap: apiv2.BootstrapConfig{
					DatastoreType: utils.Pointer("unknown"),
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				g := NewWithT(t)

				config, err := types.ClusterConfigFromBootstrapConfig(tc.bootstrap)
				g.Expect(config).To(BeZero())
				g.Expect(err).To(HaveOccurred())
			})
		}
	})
}
