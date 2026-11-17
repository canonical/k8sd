package k8s_test

import (
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"
)

func sampleClusterConfig() apiv2.UserFacingClusterConfig {
	return apiv2.UserFacingClusterConfig{
		Network: apiv2.NetworkConfig{
			Enabled: ptr.To(true),
		},
		DNS: apiv2.DNSConfig{
			Enabled:             ptr.To(true),
			UpstreamNameservers: ptr.To([]string{"8.8.8.8"}),
			ClusterDomain:       ptr.To("cluster.local"),
			ServiceIP:           ptr.To("10.152.183.10"),
		},
		Gateway: apiv2.GatewayConfig{
			Enabled: ptr.To(true),
		},
		Ingress: apiv2.IngressConfig{
			Enabled:             ptr.To(true),
			DefaultTLSSecret:    ptr.To("default/tls-secret"),
			EnableProxyProtocol: ptr.To(true),
		},
		LocalStorage: apiv2.LocalStorageConfig{
			Enabled:       ptr.To(true),
			LocalPath:     ptr.To("/var/snap/k8s/common/local-storage"),
			ReclaimPolicy: ptr.To("Delete"),
			Default:       ptr.To(true),
		},
		LoadBalancer: apiv2.LoadBalancerConfig{
			Enabled:        ptr.To(true),
			CIDRs:          ptr.To([]string{"192.168.1.0/24"}),
			L2Mode:         ptr.To(true),
			L2Interfaces:   ptr.To([]string{"eth0"}),
			BGPMode:        ptr.To(true),
			BGPLocalASN:    ptr.To(64512),
			BGPPeerAddress: ptr.To("10.0.0.1"),
			BGPPeerPort:    ptr.To(179),
			BGPPeerASN:     ptr.To(64513),
		},
	}
}

func TestK8sGetCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("get")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot connect to k8sd socket")

		code, _ := cli.execute("get")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("node status check failed")

		code, _ := cli.execute("get")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("get")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("GetClusterConfigError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetClusterConfigErr = errors.New("database read error")

		code, _ := cli.execute("get")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to get the current cluster configuration"))
	})

	t.Run("TimeoutWarning", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetClusterConfigResponse.Config = sampleClusterConfig()

		code, _ := cli.execute("get", "--timeout=1s")
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Timeout 1s is less than minimum of 3s"))
	})

	t.Run("TooManyArgs", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("get", "network", "extra")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("accepts at most 1 arg"))
	})

	t.Run("UnknownKey", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetClusterConfigResponse.Config = sampleClusterConfig()

		code, _ := cli.execute("get", "unknown.key")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring(`Unknown config key "unknown.key"`))
	})

	t.Run("AllConfigFormatsAndKubeProxyDefault", func(t *testing.T) {
		// When KubeProxyEnabled is nil, it should default to true and be scrubbed
		cfg := sampleClusterConfig()
		cfg.Network.KubeProxyEnabled = nil
		cfg.CloudProvider = ptr.To("external")
		cfg.Annotations = map[string]string{"anno": "val"}

		for _, format := range []string{"plain", "json", "yaml"} {
			g := NewWithT(t)
			cli := newTestCLI()
			cli.MockClient.GetClusterConfigResponse.Config = cfg

			code, err := cli.execute("get", "--output-format="+format)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(code).To(Equal(0))
			out := cli.Stdout.String()
			g.Expect(out).To(ContainSubstring("network"))
			// Ensure cloud-provider and annotations were scrubbed
			g.Expect(out).NotTo(ContainSubstring("external"))
			g.Expect(out).NotTo(ContainSubstring("anno"))
		}
	})

	testCases := []struct {
		key            string
		expectedOutput string
	}{
		// Feature sections
		{key: "network", expectedOutput: "kube-proxy-enabled: true"},
		{key: "dns", expectedOutput: "cluster-domain: cluster.local"},
		{key: "gateway", expectedOutput: "enabled: true"},
		{key: "ingress", expectedOutput: "default-tls-secret"},
		{key: "local-storage", expectedOutput: "local-path"},
		{key: "load-balancer", expectedOutput: "bgp-mode: true"},
		// Dotted keys
		{key: "network.enabled", expectedOutput: "true"},
		{key: "network.kube-proxy-enabled", expectedOutput: "true"},
		{key: "dns.enabled", expectedOutput: "true"},
		{key: "dns.upstream-nameservers", expectedOutput: "8.8.8.8"},
		{key: "dns.cluster-domain", expectedOutput: "cluster.local"},
		{key: "dns.service-ip", expectedOutput: "10.152.183.10"},
		{key: "gateway.enabled", expectedOutput: "true"},
		{key: "ingress.enabled", expectedOutput: "true"},
		{key: "ingress.default-tls-secret", expectedOutput: "default/tls-secret"},
		{key: "ingress.enable-proxy-protocol", expectedOutput: "true"},
		{key: "local-storage.enabled", expectedOutput: "true"},
		{key: "local-storage.local-path", expectedOutput: "/var/snap/k8s/common/local-storage"},
		{key: "local-storage.reclaim-policy", expectedOutput: "Delete"},
		{key: "local-storage.default", expectedOutput: "true"},
		{key: "load-balancer.enabled", expectedOutput: "true"},
		{key: "load-balancer.cidrs", expectedOutput: "192.168.1.0/24"},
		{key: "load-balancer.l2-mode", expectedOutput: "true"},
		{key: "load-balancer.l2-interfaces", expectedOutput: "eth0"},
		{key: "load-balancer.bgp-mode", expectedOutput: "true"},
		{key: "load-balancer.bgp-local-asn", expectedOutput: "64512"},
		{key: "load-balancer.bgp-peer-address", expectedOutput: "10.0.0.1"},
		{key: "load-balancer.bgp-peer-port", expectedOutput: "179"},
		{key: "load-balancer.bgp-peer-asn", expectedOutput: "64513"},
	}

	for _, tc := range testCases {
		t.Run("Key_"+tc.key, func(t *testing.T) {
			g := NewWithT(t)
			cli := newTestCLI()
			cli.MockClient.GetClusterConfigResponse.Config = sampleClusterConfig()

			code, err := cli.execute("get", tc.key)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(code).To(Equal(0))
			g.Expect(cli.Stdout.String()).To(ContainSubstring(tc.expectedOutput))
		})
	}
}
