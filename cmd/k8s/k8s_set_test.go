package k8s

import (
	"fmt"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	metallbAnnotations "github.com/canonical/k8s-snap-api/v2/api/annotations/metallb"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

type mapstructureTestCase struct {
	name       string
	val        string
	expectErr  bool
	assertions []types.GomegaMatcher
}

func generateMapstructureTestCasesBool(keyName string, fieldName string) []mapstructureTestCase {
	return []mapstructureTestCase{
		{
			val:        fmt.Sprintf("%s=true", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer(true))},
		},
		{
			val:        fmt.Sprintf("%s=false", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer(false))},
		},
		{
			val:        fmt.Sprintf("%s=", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer(false))},
		},
		{
			val:       fmt.Sprintf("%s=yes", keyName),
			expectErr: true,
		},
	}
}

func generateMapstructureTestCasesStringSlice(keyName string, fieldName string) []mapstructureTestCase {
	return []mapstructureTestCase{
		{
			val:        fmt.Sprintf("%s=", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{}))},
		},
		{
			val:        fmt.Sprintf("%s=[]", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{}))},
		},
		{
			val:        fmt.Sprintf("%s=100", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{"100"}))},
		},
		{
			val:        fmt.Sprintf("%s=t1", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{"t1"}))},
		},
		{
			val:        fmt.Sprintf(`%s=["t1"]`, keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{"t1"}))},
		},
		{
			val:        fmt.Sprintf("%s=[t1]", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{"t1"}))},
		},
		{
			val:        fmt.Sprintf("%s=t1, t2", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{"t1", "t2"}))},
		},
		{
			val:        fmt.Sprintf(`%s=["t1", "t2"]`, keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{"t1", "t2"}))},
		},
		{
			val:        fmt.Sprintf(`%s=[t1, t2]`, keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer([]string{"t1", "t2"}))},
		},
	}
}

func generateMapstructureTestCasesMap(keyName string, fieldName string) []mapstructureTestCase {
	return []mapstructureTestCase{
		{
			val:        fmt.Sprintf("%s=", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{})},
		},
		{
			val:        fmt.Sprintf("%s={}", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{})},
		},
		{
			val:        fmt.Sprintf("%s=k1=", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{"k1": ""})},
		},
		{
			val:        fmt.Sprintf("%s=k1=,k2=test", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{"k1": "", "k2": "test"})},
		},
		{
			val:        fmt.Sprintf("%s=k1=v1", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{"k1": "v1"})},
		},
		{
			val:        fmt.Sprintf("%s=k1=v1,k2=v2", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{"k1": "v1", "k2": "v2"})},
		},
		{
			val:        fmt.Sprintf("%s={k1: v1}", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{"k1": "v1"})},
		},
		{
			val:        fmt.Sprintf("%s={k1: v1, k2: v2}", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, map[string]string{"k1": "v1", "k2": "v2"})},
		},
		{
			val:       fmt.Sprintf("%s=k1,k2", keyName),
			expectErr: true,
		},
	}
}

func generateMapstructureTestCasesString(keyName string, fieldName string) []mapstructureTestCase {
	return []mapstructureTestCase{
		{
			val:        fmt.Sprintf("%s=", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer(""))},
		},
		{
			val:        fmt.Sprintf("%s=t1", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer("t1"))},
		},
	}
}

func generateMapstructureTestCasesInt(keyName string, fieldName string) []mapstructureTestCase {
	return []mapstructureTestCase{
		{
			val:        fmt.Sprintf("%s=", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer(0))},
		},
		{
			val:        fmt.Sprintf("%s=100", keyName),
			assertions: []types.GomegaMatcher{HaveField(fieldName, utils.Pointer(100))},
		},
		{
			val:       fmt.Sprintf("%s=notanumber", keyName),
			expectErr: true,
		},
	}
}

func Test_updateConfigMapstructure(t *testing.T) {
	for _, tcs := range [][]mapstructureTestCase{
		generateMapstructureTestCasesBool("dns.enabled", "DNS.Enabled"),
		generateMapstructureTestCasesBool("gateway.enabled", "Gateway.Enabled"),
		generateMapstructureTestCasesBool("ingress.enable-proxy-protocol", "Ingress.EnableProxyProtocol"),
		generateMapstructureTestCasesBool("ingress.enabled", "Ingress.Enabled"),
		generateMapstructureTestCasesBool("load-balancer.bgp-mode", "LoadBalancer.BGPMode"),
		generateMapstructureTestCasesBool("load-balancer.l2-mode", "LoadBalancer.L2Mode"),
		generateMapstructureTestCasesBool("load-balancer.enabled", "LoadBalancer.Enabled"),
		generateMapstructureTestCasesBool("load-balancer.enabled", "LoadBalancer.Enabled"),
		generateMapstructureTestCasesBool("local-storage.default", "LocalStorage.Default"),
		generateMapstructureTestCasesBool("local-storage.enabled", "LocalStorage.Enabled"),
		generateMapstructureTestCasesBool("metrics-server.enabled", "MetricsServer.Enabled"),
		generateMapstructureTestCasesBool("network.enabled", "Network.Enabled"),
		generateMapstructureTestCasesBool("network.kube-proxy-enabled", "Network.KubeProxyEnabled"),

		generateMapstructureTestCasesString("cloud-provider", "CloudProvider"),
		generateMapstructureTestCasesString("dns.cluster-domain", "DNS.ClusterDomain"),
		generateMapstructureTestCasesString("dns.service-ip", "DNS.ServiceIP"),
		generateMapstructureTestCasesString("ingress.default-tls-secret", "Ingress.DefaultTLSSecret"),
		generateMapstructureTestCasesString("load-balancer.bgp-peer-address", "LoadBalancer.BGPPeerAddress"),
		generateMapstructureTestCasesString("local-storage.local-path", "LocalStorage.LocalPath"),
		generateMapstructureTestCasesString("local-storage.reclaim-policy", "LocalStorage.ReclaimPolicy"),

		generateMapstructureTestCasesStringSlice("dns.upstream-nameservers", "DNS.UpstreamNameservers"),
		generateMapstructureTestCasesStringSlice("load-balancer.cidrs", "LoadBalancer.CIDRs"),
		generateMapstructureTestCasesStringSlice("load-balancer.l2-interfaces", "LoadBalancer.L2Interfaces"),

		generateMapstructureTestCasesInt("load-balancer.bgp-local-asn", "LoadBalancer.BGPLocalASN"),
		generateMapstructureTestCasesInt("load-balancer.bgp-peer-asn", "LoadBalancer.BGPPeerASN"),
		generateMapstructureTestCasesInt("load-balancer.bgp-peer-port", "LoadBalancer.BGPPeerPort"),

		generateMapstructureTestCasesMap("annotations", "Annotations"),
	} {
		for _, tc := range tcs {
			t.Run(tc.val, func(t *testing.T) {
				g := NewWithT(t)

				var cfg apiv2.UserFacingClusterConfig
				err := updateConfigMapstructure(&cfg, tc.val)
				if tc.expectErr {
					g.Expect(err).To(HaveOccurred())
				} else {
					g.Expect(err).To(Not(HaveOccurred()))
					g.Expect(cfg).To(SatisfyAll(tc.assertions...))
				}
			})
		}
	}
}

// Test_updateConfigMapstructure_annotationsYAMLBlockLiteral verifies that BGP peer
// annotations can be passed via the annotations= key using YAML block literal syntax,
// as produced by: k8s set annotations="$(cat my-annotations.yaml)"
//
// where my-annotations.yaml contains:
//
//	k8sd/v1alpha1/metallb/bgp-peers: |
//	  - peerAddress: 10.0.0.1
//	    peerASN: 65001
//	    myASN: 65000
//	k8sd/v1alpha1/metallb/advertise-all-pools: "true"
func Test_updateConfigMapstructure_annotationsYAMLBlockLiteral(t *testing.T) {
	g := NewWithT(t)

	// Simulate the value of annotations="$(cat my-annotations.yaml)".
	// The YAML block literal (|) preserves the peer list as a raw YAML string value,
	// which is then unmarshalled by neighborsFromAnnotations on the server side.
	yamlInput := `annotations=` + metallbAnnotations.AnnotationBGPPeers + `: |
  - peerAddress: 10.0.0.1
    peerASN: 65001
    myASN: 65000
` + metallbAnnotations.AnnotationAdvertiseAllPools + `: "true"
`

	var cfg apiv2.UserFacingClusterConfig
	g.Expect(updateConfigMapstructure(&cfg, yamlInput)).To(Succeed())

	// YAMLToStringMapHookFunc must have decoded the YAML into Annotations.
	g.Expect(cfg.Annotations).To(HaveKey(metallbAnnotations.AnnotationBGPPeers))
	g.Expect(cfg.Annotations).To(HaveKeyWithValue(metallbAnnotations.AnnotationAdvertiseAllPools, "true"))

	// The bgp-peers value must itself be a YAML list string, parseable downstream.
	peersYAML := cfg.Annotations[metallbAnnotations.AnnotationBGPPeers]
	g.Expect(peersYAML).To(ContainSubstring("peerAddress: 10.0.0.1"))
	g.Expect(peersYAML).To(ContainSubstring("peerASN: 65001"))
}
