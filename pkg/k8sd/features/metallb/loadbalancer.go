package metallb

import (
	"context"
	"fmt"
	"net"

	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils/control"
)

const (
	enabledMsgTmpl      = "enabled, %s mode"
	DisabledMsg         = "disabled"
	deleteFailedMsgTmpl = "Failed to delete MetalLB, the error was: %v"
	deployFailedMsgTmpl = "Failed to deploy MetalLB, the error was: %v"
)

// BGPNeighbor is an internal representation of a single MetalLB BGPPeer.
// Exported for testing purposes only.
type BGPNeighbor struct {
	PeerAddress  string
	PeerASN      int
	PeerPort     int
	MyASN        int
	NodeSelector map[string]string
}

// ValidateBGPNeighbors returns an error if any neighbor in the slice is invalid.
// Exported for testing purposes only.
func ValidateBGPNeighbors(neighbors []BGPNeighbor) error {
	for i, n := range neighbors {
		if n.PeerASN < 1 || n.PeerASN > 4294967295 {
			return fmt.Errorf("neighbor[%d]: peerASN %d out of range [1, 4294967295]", i, n.PeerASN)
		}
		if n.MyASN != 0 && (n.MyASN < 1 || n.MyASN > 4294967295) {
			return fmt.Errorf("neighbor[%d]: myASN %d out of range [1, 4294967295]", i, n.MyASN)
		}
		if n.PeerPort != 0 && (n.PeerPort < 1 || n.PeerPort > 65535) {
			return fmt.Errorf("neighbor[%d]: peerPort %d out of range [1, 65535]", i, n.PeerPort)
		}
		if _, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:179", n.PeerAddress)); err != nil {
			return fmt.Errorf("neighbor[%d]: invalid peerAddress %q: %w", i, n.PeerAddress, err)
		}
		for k := range n.NodeSelector {
			if k == "" {
				return fmt.Errorf("neighbor[%d]: nodeSelector has empty key", i)
			}
		}
	}
	return nil
}

// ApplyLoadBalancer will always return a FeatureStatus indicating the current status of the
// deployment.
// ApplyLoadBalancer returns an error if anything fails. The error is also wrapped in the .Message field of the
// returned FeatureStatus.
func ApplyLoadBalancer(ctx context.Context, snap snap.Snap, loadbalancer types.LoadBalancer, network types.Network, _ types.Annotations) (types.FeatureStatus, error) {
	if !loadbalancer.GetEnabled() {
		if err := disableLoadBalancer(ctx, snap, network); err != nil {
			err = fmt.Errorf("failed to disable LoadBalancer: %w", err)
			return types.FeatureStatus{
				Enabled: false,
				Version: ControllerImageTag,
				Message: fmt.Sprintf(deleteFailedMsgTmpl, err),
			}, err
		}
		return types.FeatureStatus{
			Enabled: false,
			Version: ControllerImageTag,
			Message: DisabledMsg,
		}, nil
	}

	if err := enableLoadBalancer(ctx, snap, loadbalancer, network); err != nil {
		err = fmt.Errorf("failed to enable LoadBalancer: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: ControllerImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, err
	}

	switch {
	case loadbalancer.GetBGPMode():
		return types.FeatureStatus{
			Enabled: true,
			Version: ControllerImageTag,
			Message: fmt.Sprintf(enabledMsgTmpl, "BGP"),
		}, nil
	case loadbalancer.GetL2Mode():
		return types.FeatureStatus{
			Enabled: true,
			Version: ControllerImageTag,
			Message: fmt.Sprintf(enabledMsgTmpl, "L2"),
		}, nil
	default:
		return types.FeatureStatus{
			Enabled: true,
			Version: ControllerImageTag,
			Message: fmt.Sprintf(enabledMsgTmpl, "Unknown"),
		}, nil
	}
}

func disableLoadBalancer(ctx context.Context, snap snap.Snap, network types.Network) error {
	m := snap.HelmClient()

	if _, err := m.Apply(ctx, ChartMetalLBLoadBalancer, helm.StateDeleted, nil); err != nil {
		return fmt.Errorf("failed to uninstall MetalLB LoadBalancer chart: %w", err)
	}

	if _, err := m.Apply(ctx, ChartMetalLB, helm.StateDeleted, nil); err != nil {
		return fmt.Errorf("failed to uninstall MetalLB chart: %w", err)
	}
	return nil
}

// BuildLoadBalancerValues constructs the Helm values map for the ck-loadbalancer chart.
// neighbors is the list of BGP peers to render; advertiseAllPools controls the
// BGPAdvertisement spec (empty spec when true, named pool when false).
// Exported for testing purposes only.
func BuildLoadBalancerValues(lb types.LoadBalancer, neighbors []BGPNeighbor, advertiseAllPools bool) map[string]any {
	cidrs := []map[string]any{}
	for _, cidr := range lb.GetCIDRs() {
		cidrs = append(cidrs, map[string]any{"cidr": cidr})
	}
	for _, ipRange := range lb.GetIPRanges() {
		cidrs = append(cidrs, map[string]any{"start": ipRange.Start, "stop": ipRange.Stop})
	}

	neighborMaps := make([]map[string]any, 0, len(neighbors))
	for _, n := range neighbors {
		nm := map[string]any{
			"peerAddress": n.PeerAddress,
			"peerASN":     n.PeerASN,
			"peerPort":    n.PeerPort,
		}
		if n.MyASN != 0 {
			nm["myASN"] = n.MyASN
		}
		if len(n.NodeSelector) > 0 {
			nm["nodeSelector"] = n.NodeSelector
		}
		neighborMaps = append(neighborMaps, nm)
	}

	return map[string]any{
		"driver": "metallb",
		"l2": map[string]any{
			"enabled":    lb.GetL2Mode(),
			"interfaces": lb.GetL2Interfaces(),
		},
		"ipPool": map[string]any{
			"cidrs": cidrs,
		},
		"bgp": map[string]any{
			"enabled":           lb.GetBGPMode(),
			"localASN":          lb.GetBGPLocalASN(),
			"neighbors":         neighborMaps,
			"advertiseAllPools": advertiseAllPools,
		},
	}
}

func enableLoadBalancer(ctx context.Context, snap snap.Snap, loadbalancer types.LoadBalancer, network types.Network) error {
	m := snap.HelmClient()

	metalLBValues := map[string]any{
		"controller": map[string]any{
			"image": map[string]any{
				"repository": controllerImageRepo,
				"tag":        ControllerImageTag,
			},
			"command": "/controller",
		},
		"speaker": map[string]any{
			"image": map[string]any{
				"repository": speakerImageRepo,
				"tag":        speakerImageTag,
			},
			"command": "/speaker",
			// TODO(neoaggelos): make frr enable/disable configurable through an annotation
			// We keep it disabled by default
			"frr": map[string]any{
				"enabled": false,
				"image": map[string]any{
					"repository": frrImageRepo,
					"tag":        frrImageTag,
				},
			},
		},
	}
	if _, err := m.Apply(ctx, ChartMetalLB, helm.StatePresent, metalLBValues); err != nil {
		return fmt.Errorf("failed to apply MetalLB configuration: %w", err)
	}

	if err := waitForRequiredLoadBalancerCRDs(ctx, snap, loadbalancer.GetBGPMode()); err != nil {
		return fmt.Errorf("failed to wait for required MetalLB CRDs: %w", err)
	}

	neighbors := []BGPNeighbor{{
		PeerAddress: loadbalancer.GetBGPPeerAddress(),
		PeerASN:     loadbalancer.GetBGPPeerASN(),
		PeerPort:    loadbalancer.GetBGPPeerPort(),
	}}
	values := BuildLoadBalancerValues(loadbalancer, neighbors, false)

	if _, err := m.Apply(ctx, ChartMetalLBLoadBalancer, helm.StatePresent, values); err != nil {
		return fmt.Errorf("failed to apply MetalLB LoadBalancer configuration: %w", err)
	}

	return nil
}

func waitForRequiredLoadBalancerCRDs(ctx context.Context, snap snap.Snap, bgpMode bool) error {
	client, err := snap.KubernetesClient("")
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return control.WaitUntilReady(ctx, func() (bool, error) {
		resourcesv1beta1, err := client.ListResourcesForGroupVersion("metallb.io/v1beta1")
		if err != nil {
			// This error is expected if the group version is not yet deployed.
			return false, nil
		}
		resourcesv1beta2, err := client.ListResourcesForGroupVersion("metallb.io/v1beta2")
		if err != nil {
			// This error is expected if the group version is not yet deployed.
			return false, nil
		}

		requiredCRDs := map[string]struct{}{
			"metallb.io/v1beta1:ipaddresspools":   {},
			"metallb.io/v1beta1:l2advertisements": {},
		}
		if bgpMode {
			requiredCRDs["metallb.io/v1beta2:bgppeers"] = struct{}{}
			requiredCRDs["metallb.io/v1beta1:bgpadvertisements"] = struct{}{}
		}

		requiredCount := len(requiredCRDs)

		for _, resource := range resourcesv1beta1.APIResources {
			if _, ok := requiredCRDs[fmt.Sprintf("metallb.io/v1beta1:%s", resource.Name)]; ok {
				requiredCount--
			}
		}

		for _, resource := range resourcesv1beta2.APIResources {
			if _, ok := requiredCRDs[fmt.Sprintf("metallb.io/v1beta2:%s", resource.Name)]; ok {
				requiredCount--
			}
		}

		return requiredCount == 0, nil
	})
}
