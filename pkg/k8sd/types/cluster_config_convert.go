package types

import (
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/utils"
)

// ClusterConfigFromBootstrapConfig converts BootstrapConfig from public API into a ClusterConfig.
func ClusterConfigFromBootstrapConfig(b apiv2.BootstrapConfig) (ClusterConfig, error) {
	config, err := ClusterConfigFromUserFacing(b.ClusterConfig)
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("invalid cluster configuration: %w", err)
	}

	// APIServer
	config.APIServer.SecurePort = b.SecurePort
	if b.DisableRBAC != nil && *b.DisableRBAC {
		config.APIServer.AuthorizationMode = utils.Pointer("AlwaysAllow")
	} else {
		config.APIServer.AuthorizationMode = utils.Pointer("Node,RBAC")
	}

	// Datastore
	switch b.GetDatastoreType() {
	case "", "etcd":
		if len(b.DatastoreServers) > 0 {
			return ClusterConfig{}, fmt.Errorf("datastore-servers needs datastore-type to be external, not %q", b.GetDatastoreType())
		}
		if b.GetDatastoreCACert() != "" {
			return ClusterConfig{}, fmt.Errorf("datastore-ca-crt needs datastore-type to be external, not %q", b.GetDatastoreType())
		}
		if b.GetDatastoreClientCert() != "" {
			return ClusterConfig{}, fmt.Errorf("datastore-client-crt needs datastore-type to be external, not %q", b.GetDatastoreType())
		}
		if b.GetDatastoreClientKey() != "" {
			return ClusterConfig{}, fmt.Errorf("datastore-client-key needs datastore-type to be external, not %q", b.GetDatastoreType())
		}

		config.Datastore = Datastore{
			Type:         utils.Pointer("etcd"),
			EtcdPort:     b.EtcdPort,
			EtcdPeerPort: b.EtcdPeerPort,
		}
	case "external":
		if len(b.DatastoreServers) == 0 {
			return ClusterConfig{}, fmt.Errorf("datastore type is external but no datastore servers were set")
		}
		if b.GetEtcdPeerPort() != 0 {
			return ClusterConfig{}, fmt.Errorf("etcd-peer-port needs datastore-type to be etcd")
		}
		if b.GetEtcdPort() != 0 {
			return ClusterConfig{}, fmt.Errorf("etcd-port needs datastore-type to be etcd")
		}
		config.Datastore = Datastore{
			Type:               utils.Pointer("external"),
			ExternalServers:    utils.Pointer(b.DatastoreServers),
			ExternalCACert:     b.DatastoreCACert,
			ExternalClientCert: b.DatastoreClientCert,
			ExternalClientKey:  b.DatastoreClientKey,
		}
	default:
		return ClusterConfig{}, fmt.Errorf("unknown datastore type specified in bootstrap config %q", b.GetDatastoreType())
	}

	// Network
	config.Network.PodCIDR = b.PodCIDR
	config.Network.ServiceCIDR = b.ServiceCIDR

	// Kubelet
	config.Kubelet.CloudProvider = b.ClusterConfig.CloudProvider
	if len(b.ControlPlaneTaints) != 0 {
		config.Kubelet.ControlPlaneTaints = utils.Pointer(b.ControlPlaneTaints)
	}

	return config, nil
}

// ClusterConfigFromUserFacing converts UserFacingClusterConfig from public API into a ClusterConfig.
func ClusterConfigFromUserFacing(u apiv2.UserFacingClusterConfig) (ClusterConfig, error) {
	cidrs, ipRanges, err := loadBalancerCIDRsFromAPI(u.LoadBalancer.CIDRs)
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("invalid load-balancer.cidrs: %w", err)
	}

	return ClusterConfig{
		Annotations: Annotations(u.Annotations),
		Kubelet: Kubelet{
			ClusterDNS:    u.DNS.ServiceIP,
			ClusterDomain: u.DNS.ClusterDomain,
			CloudProvider: u.CloudProvider,
		},
		Network: Network{
			Enabled:       u.Network.Enabled,
			KubeProxyFree: u.Network.KubeProxyFree,
		},
		DNS: DNS{
			Enabled:             u.DNS.Enabled,
			UpstreamNameservers: u.DNS.UpstreamNameservers,
		},
		Ingress: Ingress{
			Enabled:             u.Ingress.Enabled,
			DefaultTLSSecret:    u.Ingress.DefaultTLSSecret,
			EnableProxyProtocol: u.Ingress.EnableProxyProtocol,
		},
		LoadBalancer: LoadBalancer{
			Enabled:        u.LoadBalancer.Enabled,
			CIDRs:          cidrs,
			IPRanges:       ipRanges,
			L2Mode:         u.LoadBalancer.L2Mode,
			L2Interfaces:   u.LoadBalancer.L2Interfaces,
			BGPMode:        u.LoadBalancer.BGPMode,
			BGPLocalASN:    u.LoadBalancer.BGPLocalASN,
			BGPPeerAddress: u.LoadBalancer.BGPPeerAddress,
			BGPPeerASN:     u.LoadBalancer.BGPPeerASN,
			BGPPeerPort:    u.LoadBalancer.BGPPeerPort,
		},
		LocalStorage: LocalStorage{
			Enabled:       u.LocalStorage.Enabled,
			LocalPath:     u.LocalStorage.LocalPath,
			ReclaimPolicy: u.LocalStorage.ReclaimPolicy,
			Default:       u.LocalStorage.Default,
		},
		MetricsServer: MetricsServer{
			Enabled: u.MetricsServer.Enabled,
		},
		Gateway: Gateway{
			Enabled: u.Gateway.Enabled,
		},
	}, nil
}

// ToUserFacing converts a ClusterConfig to a UserFacingClusterConfig from the public API.
func (c ClusterConfig) ToUserFacing() apiv2.UserFacingClusterConfig {
	return apiv2.UserFacingClusterConfig{
		Network: apiv2.NetworkConfig{
			Enabled:       c.Network.Enabled,
			KubeProxyFree: c.Network.KubeProxyFree,
		},
		DNS: apiv2.DNSConfig{
			Enabled:             c.DNS.Enabled,
			ClusterDomain:       c.Kubelet.ClusterDomain,
			ServiceIP:           c.Kubelet.ClusterDNS,
			UpstreamNameservers: c.DNS.UpstreamNameservers,
		},
		Ingress: apiv2.IngressConfig{
			Enabled:             c.Ingress.Enabled,
			DefaultTLSSecret:    c.Ingress.DefaultTLSSecret,
			EnableProxyProtocol: c.Ingress.EnableProxyProtocol,
		},
		LoadBalancer: apiv2.LoadBalancerConfig{
			Enabled:        c.LoadBalancer.Enabled,
			CIDRs:          loadBalancerCIDRsToAPI(c.LoadBalancer.CIDRs, c.LoadBalancer.IPRanges),
			L2Mode:         c.LoadBalancer.L2Mode,
			L2Interfaces:   c.LoadBalancer.L2Interfaces,
			BGPMode:        c.LoadBalancer.BGPMode,
			BGPLocalASN:    c.LoadBalancer.BGPLocalASN,
			BGPPeerAddress: c.LoadBalancer.BGPPeerAddress,
			BGPPeerASN:     c.LoadBalancer.BGPPeerASN,
			BGPPeerPort:    c.LoadBalancer.BGPPeerPort,
		},
		LocalStorage: apiv2.LocalStorageConfig{
			Enabled:       c.LocalStorage.Enabled,
			LocalPath:     c.LocalStorage.LocalPath,
			ReclaimPolicy: c.LocalStorage.ReclaimPolicy,
			Default:       c.LocalStorage.Default,
		},
		MetricsServer: apiv2.MetricsServerConfig{
			Enabled: c.MetricsServer.Enabled,
		},
		Gateway: apiv2.GatewayConfig{
			Enabled: c.Gateway.Enabled,
		},
		CloudProvider: c.Kubelet.CloudProvider,
		Annotations:   map[string]string(c.Annotations),
	}
}
