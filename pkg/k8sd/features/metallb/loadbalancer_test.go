package metallb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/canonical/k8sd/pkg/client/helm"
	helmmock "github.com/canonical/k8sd/pkg/client/helm/mock"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features/metallb"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
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

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).To(MatchError(applyErr))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Version).To(Equal(metallb.ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(metallb.ChartMetalLBLoadBalancer))
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

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(Equal(metallb.DisabledMsg))
		g.Expect(status.Version).To(Equal(metallb.ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))

		firstCallArgs := helmM.ApplyCalledWith[0]
		g.Expect(firstCallArgs.Chart).To(Equal(metallb.ChartMetalLBLoadBalancer))
		g.Expect(firstCallArgs.State).To(Equal(helm.StateDeleted))
		g.Expect(firstCallArgs.Values).To(BeNil())

		secondCallArgs := helmM.ApplyCalledWith[1]
		g.Expect(secondCallArgs.Chart).To(Equal(metallb.ChartMetalLB))
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

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).To(MatchError(applyErr))
		g.Expect(status.Enabled).To(BeFalse())
		g.Expect(status.Message).To(ContainSubstring(applyErr.Error()))
		g.Expect(status.Version).To(Equal(metallb.ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(1))

		callArgs := helmM.ApplyCalledWith[0]
		g.Expect(callArgs.Chart).To(Equal(metallb.ChartMetalLB))
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

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Version).To(Equal(metallb.ControllerImageTag))
		g.Expect(helmM.ApplyCalledWith).To(HaveLen(2))

		firstCallArgs := helmM.ApplyCalledWith[0]
		g.Expect(firstCallArgs.Chart).To(Equal(metallb.ChartMetalLB))
		g.Expect(firstCallArgs.State).To(Equal(helm.StatePresent))
		// we don't validate values since it's just a static struct
		// and won't be changed by configurations
		g.Expect(firstCallArgs.Values).ToNot(BeNil())

		secondCallArgs := helmM.ApplyCalledWith[1]
		g.Expect(secondCallArgs.Chart).To(Equal(metallb.ChartMetalLBLoadBalancer))
		g.Expect(secondCallArgs.State).To(Equal(helm.StatePresent))
		validateLoadBalancerValues(g, secondCallArgs.Values, lbCfg)
	})
}

func validateLoadBalancerValues(g Gomega, values map[string]interface{}, lbCfg types.LoadBalancer) {
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
	g.Expect(neighbors).To(HaveLen(1))
	neighbor := neighbors[0]
	g.Expect(neighbor["peerAddress"]).To(Equal(lbCfg.GetBGPPeerAddress()))
	g.Expect(neighbor["peerASN"]).To(Equal(lbCfg.GetBGPPeerASN()))
	g.Expect(neighbor["peerPort"]).To(Equal(lbCfg.GetBGPPeerPort()))
}

func TestConfigMapOverrides(t *testing.T) {
	lbCfg := types.LoadBalancer{
		Enabled: ptr.To(true),
		L2Mode:  ptr.To(true),
	}

	newSnap := func(objects ...k8sruntime.Object) *snapmock.Snap {
		clientset := fake.NewSimpleClientset(objects...)
		fd, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		if ok {
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
		}
		return &snapmock.Snap{
			Mock: snapmock.Mock{
				HelmClient:       &helmmock.Mock{},
				KubernetesClient: &kubernetes.Client{Interface: clientset},
			},
		}
	}

	configMap := func(valuesYAML string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-metallb-values", Namespace: "kube-system"},
			Data:       map[string]string{"values": valuesYAML},
		}
	}

	t.Run("NoConfigMap", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap()

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).NotTo(ContainSubstring("warning"))
		helmValues := snapM.Mock.HelmClient.(*helmmock.Mock).ApplyCalledWith[0].Values
		speaker := helmValues["speaker"].(map[string]any)
		frr := speaker["frr"].(map[string]any)
		g.Expect(frr["enabled"]).To(BeFalse())
	})

	t.Run("OverrideScalarValue", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("speaker:\n  frr:\n    enabled: true\n"))

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		helmValues := snapM.Mock.HelmClient.(*helmmock.Mock).ApplyCalledWith[0].Values
		speaker := helmValues["speaker"].(map[string]any)
		frr := speaker["frr"].(map[string]any)
		g.Expect(frr["enabled"]).To(BeTrue())
	})

	t.Run("DeepMergePreservesUnrelatedKeys", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("speaker:\n  frr:\n    enabled: true\n"))

		_, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).NotTo(HaveOccurred())
		helmValues := snapM.Mock.HelmClient.(*helmmock.Mock).ApplyCalledWith[0].Values
		speaker := helmValues["speaker"].(map[string]any)
		// command key not in override — should keep default.
		g.Expect(speaker["command"]).To(Equal("/speaker"))
	})

	t.Run("InvalidYAMLFallsBackToDefaults", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("this: is: not: valid: yaml: :::"))

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		// ApplyLoadBalancer should not fail — it uses defaults and surfaces the warning in status.
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).To(ContainSubstring("warning:"))
		g.Expect(status.Message).To(ContainSubstring("failed to parse configmap values"))
	})

	t.Run("ValidOverrideHasNoWarningInStatus", func(t *testing.T) {
		g := NewWithT(t)
		snapM := newSnap(configMap("speaker:\n  frr:\n    enabled: true\n"))

		status, err := metallb.ApplyLoadBalancer(context.Background(), snapM, lbCfg, types.Network{}, nil)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status.Enabled).To(BeTrue())
		g.Expect(status.Message).NotTo(ContainSubstring("warning"))
	})
}
