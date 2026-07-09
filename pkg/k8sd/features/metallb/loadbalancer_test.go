package metallb

import (
	"context"
	"errors"
	"testing"

	"github.com/canonical/k8sd/pkg/client/helm"
	helmmock "github.com/canonical/k8sd/pkg/client/helm/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
)

func TestDisabled(t *testing.T) {
	t.Run("HelmApplyFails", func(t *testing.T) {
		g := NewWithT(t)

		applyErr := errors.New("failed to apply")
		helmM := &helmmock.Mock{
			ApplyErr: applyErr,
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		lbCfg := types.LoadBalancer{
			Enabled: ptr.To(false),
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).To(MatchError(applyErr))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Version).To(Equal(ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(ChartMetalLBLoadBalancer))
		g.Expect(callArgs.State).To(Equal(helm.StateDeleted))
		g.Expect(callArgs.Values).To(BeNil())
	})
	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		lbCfg := types.LoadBalancer{
			Enabled: ptr.To(false),
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(Equal(DisabledMsg))
		g.Expect(status.Version).To(Equal(ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))

		firstCallArgs := helmM.ApplyCalledWith[0]
		g.Expect(firstCallArgs.Chart).To(Equal(ChartMetalLBLoadBalancer))
		g.Expect(firstCallArgs.State).To(Equal(helm.StateDeleted))
		g.Expect(firstCallArgs.Values).To(BeNil())

		secondCallArgs := helmM.ApplyCalledWith[1]
		g.Expect(secondCallArgs.Chart).To(Equal(ChartMetalLB))
		g.Expect(secondCallArgs.State).To(Equal(helm.StateDeleted))
		g.Expect(secondCallArgs.Values).To(BeNil())
	})
}

func TestEnabled(t *testing.T) {
	t.Run("HelmApplyFails", func(t *testing.T) {
		g := NewWithT(t)

		applyErr := errors.New("failed to apply")
		helmM := &helmmock.Mock{
			ApplyErr: applyErr,
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
			},
		}
		lbCfg := types.LoadBalancer{
			Enabled: ptr.To(true),
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).To(MatchError(applyErr))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Version).To(Equal(ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(ChartMetalLB))
		g.Expect(callArgs.State).To(Equal(helm.StatePresent))
		// we don't validate values since it's just a static struct
		// and won't be changed by configurations
		g.Expect(callArgs.Values).ToNot(BeNil())
	})
	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		clientset := fake.NewSimpleClientset()
		fd, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		g.Expect(ok).To(BeTrue())
		fd.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "metallb.io/v1beta1",
				APIResources: []metav1.APIResource{
					{Name: "ipaddresspools"},
					{Name: "l2advertisements"},
					{Name: "bgpadvertisements"},
				},
			},
			{
				GroupVersion: "metallb.io/v1beta2",
				APIResources: []metav1.APIResource{
					{Name: "bgppeers"},
				},
			},
		}
		snapM := &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
				KubernetesClient: &kubernetes.Client{
					Interface: clientset,
				},
			},
		}
		lbCfg := types.LoadBalancer{
			Enabled: ptr.To(true),
			// setting both modes to true for testing purposes
			L2Mode:         ptr.To(true),
			L2Interfaces:   ptr.To([]string{"eth0", "eth1"}),
			BGPMode:        ptr.To(true),
			BGPLocalASN:    ptr.To(64512),
			BGPPeerAddress: ptr.To("10.0.0.1/32"),
			BGPPeerASN:     ptr.To(64513),
			BGPPeerPort:    ptr.To(179),
			CIDRs:          ptr.To([]string{"192.0.2.0/24"}),
			IPRanges: ptr.To([]types.LoadBalancer_IPRange{
				{Start: "20.0.20.100", Stop: "20.0.20.200"},
			}),
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Version).To(Equal(ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))

		firstCallArgs := helmM.ApplyCalledWith[0]
		g.Expect(firstCallArgs.Chart).To(Equal(ChartMetalLB))
		g.Expect(firstCallArgs.State).To(Equal(helm.StatePresent))
		// we don't validate values since it's just a static struct
		// and won't be changed by configurations
		g.Expect(firstCallArgs.Values).ToNot(BeNil())

		secondCallArgs := helmM.ApplyCalledWith[1]
		g.Expect(secondCallArgs.Chart).To(Equal(ChartMetalLBLoadBalancer))
		g.Expect(secondCallArgs.State).To(Equal(helm.StatePresent))
		expectedNeighbors := []map[string]any{
			{
				"peerAddress": lbCfg.GetBGPPeerAddress(),
				"peerASN":     lbCfg.GetBGPPeerASN(),
				"peerPort":    lbCfg.GetBGPPeerPort(),
			},
		}
		validateLoadBalancerValues(g, secondCallArgs.Values, lbCfg, expectedNeighbors)
	})
}

func validateLoadBalancerValues(g Gomega, values map[string]interface{}, lbCfg types.LoadBalancer, expectedNeighbors []map[string]any) {
	l2 := values["l2"].(map[string]any)
	g.Expect(l2["enabled"]).To(Equal(lbCfg.GetL2Mode()))
	g.Expect(l2["interfaces"]).To(Equal(lbCfg.GetL2Interfaces()))

	ipPoolCIDRs := values["ipPool"].(map[string]any)["cidrs"].([]map[string]any)
	g.Expect(ipPoolCIDRs).To(HaveLen(len(lbCfg.GetCIDRs()) + len(lbCfg.GetIPRanges())))
	for _, cidr := range lbCfg.GetCIDRs() {
		g.Expect(ipPoolCIDRs).To(ContainElement(map[string]any{"cidr": cidr}))
	}
	for _, ipRange := range lbCfg.GetIPRanges() {
		g.Expect(ipPoolCIDRs).To(ContainElement(map[string]any{"start": ipRange.Start, "stop": ipRange.Stop}))
	}

	bgp := values["bgp"].(map[string]any)
	g.Expect(bgp["enabled"]).To(Equal(lbCfg.GetBGPMode()))
	g.Expect(bgp["localASN"]).To(Equal(lbCfg.GetBGPLocalASN()))
	neighbors := bgp["neighbors"].([]map[string]any)
	g.Expect(neighbors).To(HaveLen(len(expectedNeighbors)))
	for i, expected := range expectedNeighbors {
		actual := neighbors[i]
		for k, v := range expected {
			g.Expect(actual[k]).To(Equal(v), "neighbor[%d] key %s", i, k)
		}
	}
}

func TestBuildLoadBalancerValues(t *testing.T) {
	baseLB := types.LoadBalancer{
		BGPMode:     ptr.To(true),
		BGPLocalASN: ptr.To(64512),
		CIDRs:       ptr.To([]string{"10.0.0.0/24"}),
	}

	t.Run("SinglePeer", func(t *testing.T) {
		g := NewWithT(t)

		neighbors := []bgpNeighbor{{
			peerAddress:  "10.0.0.1",
			peerASN:      64513,
			peerPort:     179,
			myASN:        65099,
			nodeSelector: map[string]string{"zone": "a"},
		}}

		values := buildLoadBalancerValues(baseLB, neighbors, true)

		bgp := values["bgp"].(map[string]any)
		g.Expect(bgp["enabled"]).To(Equal(true))
		g.Expect(bgp["localASN"]).To(Equal(64512))
		g.Expect(bgp["advertiseAllPools"]).To(Equal(true))

		ns := bgp["neighbors"].([]map[string]any)
		g.Expect(ns).To(HaveLen(1))
		g.Expect(ns[0]["peerAddress"]).To(Equal("10.0.0.1"))
		g.Expect(ns[0]["peerASN"]).To(Equal(64513))
		g.Expect(ns[0]["peerPort"]).To(Equal(179))
		g.Expect(ns[0]["myASN"]).To(Equal(65099))
		g.Expect(ns[0]["nodeSelector"]).To(Equal(map[string]string{"zone": "a"}))
	})

	t.Run("OptionalFieldsOmitted", func(t *testing.T) {
		g := NewWithT(t)

		// myASN=0 and empty nodeSelector must not appear in the output map.
		neighbors := []bgpNeighbor{{peerAddress: "10.0.0.1", peerASN: 64513}}
		values := buildLoadBalancerValues(baseLB, neighbors, false)

		bgp := values["bgp"].(map[string]any)
		ns := bgp["neighbors"].([]map[string]any)
		g.Expect(ns).To(HaveLen(1))
		_, hasMyASN := ns[0]["myASN"]
		g.Expect(hasMyASN).To(BeFalse())
		_, hasNodeSelector := ns[0]["nodeSelector"]
		g.Expect(hasNodeSelector).To(BeFalse())
		g.Expect(bgp["advertiseAllPools"]).To(Equal(false))
	})
}

func TestValidateBGPNeighbors(t *testing.T) {
	valid := bgpNeighbor{peerAddress: "10.0.0.1", peerASN: 64513}

	t.Run("Valid", func(t *testing.T) {
		g := NewWithT(t)
		cases := []bgpNeighbor{
			valid,
			{peerAddress: "2001:db8::1", peerASN: 64513},           // IPv6
			{peerAddress: "10.0.0.1", peerASN: 64513, myASN: 0},    // myASN=0 allowed (inherit)
			{peerAddress: "10.0.0.1", peerASN: 64513, peerPort: 0}, // peerPort=0 allowed (inherit)
			{peerAddress: "10.0.0.1", peerASN: 64513, peerPort: 179, myASN: 65000, nodeSelector: map[string]string{"zone": "a"}},
		}
		for _, n := range cases {
			g.Expect(validateBGPNeighbors([]bgpNeighbor{n})).To(Succeed())
		}
	})

	t.Run("Invalid", func(t *testing.T) {
		g := NewWithT(t)
		cases := []struct {
			neighbor bgpNeighbor
			wantErr  string
		}{
			{bgpNeighbor{peerAddress: "10.0.0.1", peerASN: 0}, "peerASN 0 out of range"},
			{bgpNeighbor{peerAddress: "10.0.0.1", peerASN: 4294967296}, "peerASN 4294967296 out of range"},
			{bgpNeighbor{peerAddress: "10.0.0.1", peerASN: 64513, myASN: -1}, "myASN -1 out of range"},
			{bgpNeighbor{peerAddress: "10.0.0.1", peerASN: 64513, peerPort: 65536}, "peerPort 65536 out of range"},
			{bgpNeighbor{peerAddress: "not-an-ip", peerASN: 64513}, "invalid peerAddress"},
			{bgpNeighbor{peerAddress: "256.0.0.1", peerASN: 64513}, "invalid peerAddress"},
			{bgpNeighbor{peerAddress: "10.0.0.1", peerASN: 64513, nodeSelector: map[string]string{"": "v"}}, "nodeSelector has empty key"},
		}
		for _, tc := range cases {
			err := validateBGPNeighbors([]bgpNeighbor{tc.neighbor})
			g.Expect(err).To(HaveOccurred(), "expected error for %+v", tc.neighbor)
			g.Expect(err.Error()).To(ContainSubstring(tc.wantErr))
		}
	})
}

func TestAnnotationParsing(t *testing.T) {
	t.Run("AnnotationAbsent", func(t *testing.T) {
		g := NewWithT(t)

		neighbors, advertiseAll, active, err := neighborsFromAnnotations(nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(active).To(BeFalse())
		g.Expect(neighbors).To(BeNil())
		g.Expect(advertiseAll).To(BeFalse())

		// Also test with empty map
		neighbors, advertiseAll, active, err = neighborsFromAnnotations(types.Annotations{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(active).To(BeFalse())
		g.Expect(neighbors).To(BeNil())
		g.Expect(advertiseAll).To(BeFalse())
	})

	t.Run("ThreePeers", func(t *testing.T) {
		g := NewWithT(t)

		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": `
- peerAddress: 10.116.3.164
  peerASN: 65001
  nodeSelector:
    topology.kubernetes.io/zone: i1
- peerAddress: 10.116.3.165
  peerASN: 65002
  nodeSelector:
    topology.kubernetes.io/zone: i2
- peerAddress: 10.116.3.166
  peerASN: 65003
  nodeSelector:
    topology.kubernetes.io/zone: i3
`,
		}

		neighbors, advertiseAll, active, err := neighborsFromAnnotations(annotations)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(active).To(BeTrue())
		g.Expect(advertiseAll).To(BeFalse())
		g.Expect(neighbors).To(HaveLen(3))

		g.Expect(neighbors[0].peerAddress).To(Equal("10.116.3.164"))
		g.Expect(neighbors[0].peerASN).To(Equal(65001))
		g.Expect(neighbors[0].nodeSelector).To(Equal(map[string]string{"topology.kubernetes.io/zone": "i1"}))

		g.Expect(neighbors[1].peerAddress).To(Equal("10.116.3.165"))
		g.Expect(neighbors[1].peerASN).To(Equal(65002))
		g.Expect(neighbors[1].nodeSelector).To(Equal(map[string]string{"topology.kubernetes.io/zone": "i2"}))

		g.Expect(neighbors[2].peerAddress).To(Equal("10.116.3.166"))
		g.Expect(neighbors[2].peerASN).To(Equal(65003))
		g.Expect(neighbors[2].nodeSelector).To(Equal(map[string]string{"topology.kubernetes.io/zone": "i3"}))
	})

	t.Run("MalformedYAML", func(t *testing.T) {
		g := NewWithT(t)

		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": "not: valid: yaml: [{",
		}

		neighbors, advertiseAll, active, err := neighborsFromAnnotations(annotations)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse bgp-peers annotation"))
		g.Expect(active).To(BeTrue())
		g.Expect(neighbors).To(BeNil())
		g.Expect(advertiseAll).To(BeFalse())
	})

	t.Run("AdvertiseAllPoolsTrue", func(t *testing.T) {
		g := NewWithT(t)

		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": `
- peerAddress: 10.0.0.1
  peerASN: 65001
`,
			"k8sd/v1alpha1/metallb/advertise-all-pools": "true",
		}

		neighbors, advertiseAll, active, err := neighborsFromAnnotations(annotations)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(active).To(BeTrue())
		g.Expect(advertiseAll).To(BeTrue())
		g.Expect(neighbors).To(HaveLen(1))
	})

	t.Run("AdvertiseAllPoolsInvalid", func(t *testing.T) {
		g := NewWithT(t)

		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": `
- peerAddress: 10.0.0.1
  peerASN: 65001
`,
			"k8sd/v1alpha1/metallb/advertise-all-pools": "notabool",
		}

		neighbors, advertiseAll, active, err := neighborsFromAnnotations(annotations)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse advertise-all-pools annotation"))
		g.Expect(active).To(BeTrue())
		g.Expect(neighbors).To(BeNil())
		g.Expect(advertiseAll).To(BeFalse())
	})

	t.Run("EmptyPeersList", func(t *testing.T) {
		g := NewWithT(t)

		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": "[]",
		}

		neighbors, advertiseAll, active, err := neighborsFromAnnotations(annotations)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(active).To(BeTrue())
		g.Expect(advertiseAll).To(BeFalse())
		g.Expect(neighbors).To(HaveLen(0))
	})
}

func TestApplyLoadBalancerWithAnnotations(t *testing.T) {
	// Helper to build fake snap with helm and kubernetes mocks
	buildFakeSnap := func(helmM *helmmock.Mock) *snapmock.Snap {
		clientset := fake.NewSimpleClientset()
		fd, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		if !ok {
			panic("failed to cast to FakeDiscovery")
		}
		fd.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "metallb.io/v1beta1",
				APIResources: []metav1.APIResource{
					{Name: "ipaddresspools"},
					{Name: "l2advertisements"},
					{Name: "bgpadvertisements"},
				},
			},
			{
				GroupVersion: "metallb.io/v1beta2",
				APIResources: []metav1.APIResource{
					{Name: "bgppeers"},
				},
			},
		}
		return &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient: helmM,
				KubernetesClient: &kubernetes.Client{
					Interface: clientset,
				},
			},
		}
	}

	t.Run("MultiPeerAnnotation", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := buildFakeSnap(helmM)
		lbCfg := types.LoadBalancer{
			Enabled:     ptr.To(true),
			BGPMode:     ptr.To(true),
			BGPLocalASN: ptr.To(64512),
			CIDRs:       ptr.To([]string{"192.0.2.0/24"}),
		}
		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": `
- peerAddress: 10.116.3.164
  peerASN: 65001
  peerPort: 179
  nodeSelector:
    topology.kubernetes.io/zone: i1
- peerAddress: 10.116.3.165
  peerASN: 65002
  peerPort: 179
  nodeSelector:
    topology.kubernetes.io/zone: i2
- peerAddress: 10.116.3.166
  peerASN: 65003
  peerPort: 179
  nodeSelector:
    topology.kubernetes.io/zone: i3
`,
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, annotations)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(Equal("enabled, BGP mode (alpha)"))

		g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))
		lbValues := helmM.ApplyCalledWith[1].Values
		bgp := lbValues["bgp"].(map[string]any)
		neighbors := bgp["neighbors"].([]map[string]any)
		g.Expect(neighbors).To(HaveLen(3))

		g.Expect(neighbors[0]["peerAddress"]).To(Equal("10.116.3.164"))
		g.Expect(neighbors[0]["peerASN"]).To(Equal(65001))
		g.Expect(neighbors[0]["nodeSelector"]).To(Equal(map[string]string{"topology.kubernetes.io/zone": "i1"}))

		g.Expect(neighbors[1]["peerAddress"]).To(Equal("10.116.3.165"))
		g.Expect(neighbors[1]["peerASN"]).To(Equal(65002))
		g.Expect(neighbors[1]["nodeSelector"]).To(Equal(map[string]string{"topology.kubernetes.io/zone": "i2"}))

		g.Expect(neighbors[2]["peerAddress"]).To(Equal("10.116.3.166"))
		g.Expect(neighbors[2]["peerASN"]).To(Equal(65003))
		g.Expect(neighbors[2]["nodeSelector"]).To(Equal(map[string]string{"topology.kubernetes.io/zone": "i3"}))

		g.Expect(bgp["advertiseAllPools"]).To(Equal(false))
	})

	t.Run("AnnotationReplacesTypedKeys", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := buildFakeSnap(helmM)
		lbCfg := types.LoadBalancer{
			Enabled:        ptr.To(true),
			BGPMode:        ptr.To(true),
			BGPLocalASN:    ptr.To(64512),
			BGPPeerAddress: ptr.To("10.0.0.99"),
			BGPPeerASN:     ptr.To(64999),
			BGPPeerPort:    ptr.To(179),
			CIDRs:          ptr.To([]string{"192.0.2.0/24"}),
		}
		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": `
- peerAddress: 10.116.3.164
  peerASN: 65001
- peerAddress: 10.116.3.165
  peerASN: 65002
- peerAddress: 10.116.3.166
  peerASN: 65003
`,
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, annotations)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(Equal("enabled, BGP mode (alpha) - warning: single-peer typed keys are ignored"))

		g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))
		lbValues := helmM.ApplyCalledWith[1].Values
		bgp := lbValues["bgp"].(map[string]any)
		neighbors := bgp["neighbors"].([]map[string]any)
		g.Expect(neighbors).To(HaveLen(3))

		// Verify annotation neighbors are used, not typed keys
		g.Expect(neighbors[0]["peerAddress"]).To(Equal("10.116.3.164"))
		g.Expect(neighbors[1]["peerAddress"]).To(Equal("10.116.3.165"))
		g.Expect(neighbors[2]["peerAddress"]).To(Equal("10.116.3.166"))
	})

	t.Run("InvalidAnnotation_DegradedStatus", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := buildFakeSnap(helmM)
		lbCfg := types.LoadBalancer{
			Enabled:     ptr.To(true),
			BGPMode:     ptr.To(true),
			BGPLocalASN: ptr.To(64512),
			CIDRs:       ptr.To([]string{"192.0.2.0/24"}),
		}
		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": "not: valid: yaml: [{",
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, annotations)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid BGP peer annotation"))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring("failed to parse bgp-peers annotation"))
	})

	t.Run("AnnotationValidationFail", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := buildFakeSnap(helmM)
		lbCfg := types.LoadBalancer{
			Enabled:     ptr.To(true),
			BGPMode:     ptr.To(true),
			BGPLocalASN: ptr.To(64512),
			CIDRs:       ptr.To([]string{"192.0.2.0/24"}),
		}
		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": `
- peerAddress: 10.0.0.1
  peerASN: 0
`,
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, annotations)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid BGP peers"))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring("invalid BGP peers"))
	})

	t.Run("AdvertiseAllPoolsAnnotation", func(t *testing.T) {
		g := NewWithT(t)

		helmM := &helmmock.Mock{}
		snapM := buildFakeSnap(helmM)
		lbCfg := types.LoadBalancer{
			Enabled:     ptr.To(true),
			BGPMode:     ptr.To(true),
			BGPLocalASN: ptr.To(64512),
			CIDRs:       ptr.To([]string{"192.0.2.0/24"}),
		}
		annotations := types.Annotations{
			"k8sd/v1alpha1/metallb/bgp-peers": `
- peerAddress: 10.0.0.1
  peerASN: 65001
`,
			"k8sd/v1alpha1/metallb/advertise-all-pools": "true",
		}

		status, err := ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, annotations)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(Equal("enabled, BGP mode (alpha)"))

		g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))
		lbValues := helmM.ApplyCalledWith[1].Values
		bgp := lbValues["bgp"].(map[string]any)
		g.Expect(bgp["advertiseAllPools"]).To(Equal(true))
	})
}
