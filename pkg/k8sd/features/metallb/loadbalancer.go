package metallb

import (
	"context"
	"fmt"
	"net"
	"strconv"

	metallbAnnotations "github.com/canonical/k8s-snap-api/v2/api/annotations/metallb"
	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils/control"
	"gopkg.in/yaml.v2"
)

const (
	enabledMsgTmpl      = "enabled, %s mode"
	DisabledMsg         = "disabled"
	deleteFailedMsgTmpl = "Failed to delete MetalLB, the error was: %v"
	deployFailedMsgTmpl = "Failed to deploy MetalLB, the error was: %v"
)

// bgpNeighbor is an internal representation of a single MetalLB BGPPeer.
type bgpNeighbor struct {
	peerAddress  string
	peerASN      int
	peerPort     int
	myASN        int
	nodeSelector map[string]string
}

// validateBGPNeighbors returns an error if any neighbor in the slice is invalid.
func validateBGPNeighbors(neighbors []bgpNeighbor) error {
	for i, n := range neighbors {
		if n.peerASN < 1 || n.peerASN > 4294967295 {
			return fmt.Errorf("neighbor[%d]: peerASN %d out of range [1, 4294967295]", i, n.peerASN)
		}
		if n.myASN != 0 && (n.myASN < 1 || n.myASN > 4294967295) {
			return fmt.Errorf("neighbor[%d]: myASN %d out of range [1, 4294967295]", i, n.myASN)
		}
		if n.peerPort != 0 && (n.peerPort < 1 || n.peerPort > 65535) {
			return fmt.Errorf("neighbor[%d]: peerPort %d out of range [1, 65535]", i, n.peerPort)
		}
		if _, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:179", n.peerAddress)); err != nil {
			return fmt.Errorf("neighbor[%d]: invalid peerAddress %q: %w", i, n.peerAddress, err)
		}
		for k := range n.nodeSelector {
			if k == "" {
				return fmt.Errorf("neighbor[%d]: nodeSelector has empty key", i)
			}
		}
	}
	return nil
}

// neighborsFromAnnotations parses multi-peer BGP configuration from annotations.
// It returns the neighbor slice, advertiseAllPools flag, a boolean indicating whether
// the annotation path was active, and any parse error.
// If the bgp-peers annotation is absent, returns (nil, false, false, nil).
func neighborsFromAnnotations(annotations types.Annotations) ([]bgpNeighbor, bool, bool, error) {
	peersYAML, hasPeers := annotations[metallbAnnotations.AnnotationBGPPeers]
	if !hasPeers {
		return nil, false, false, nil
	}

	type peerYAML struct {
		PeerAddress  string            `yaml:"peerAddress"`
		PeerASN      int               `yaml:"peerASN"`
		PeerPort     int               `yaml:"peerPort"`
		MyASN        int               `yaml:"myASN"`
		NodeSelector map[string]string `yaml:"nodeSelector"`
	}
	var peers []peerYAML
	if err := yaml.Unmarshal([]byte(peersYAML), &peers); err != nil {
		return nil, false, true, fmt.Errorf("failed to parse bgp-peers annotation: %w", err)
	}
	neighbors := make([]bgpNeighbor, len(peers))
	for i, p := range peers {
		neighbors[i] = bgpNeighbor{
			peerAddress:  p.PeerAddress,
			peerASN:      p.PeerASN,
			peerPort:     p.PeerPort,
			myASN:        p.MyASN,
			nodeSelector: p.NodeSelector,
		}
	}

	advertiseAll := false
	if v, ok := annotations[metallbAnnotations.AnnotationAdvertiseAllPools]; ok {
		var err error
		advertiseAll, err = strconv.ParseBool(v)
		if err != nil {
			return nil, false, true, fmt.Errorf("failed to parse advertise-all-pools annotation %q: %w", v, err)
		}
	}

	return neighbors, advertiseAll, true, nil
}

// ApplyLoadBalancer will always return a FeatureStatus indicating the current status of the
// deployment.
// ApplyLoadBalancer returns an error if anything fails. The error is also wrapped in the .Message field of the
// returned FeatureStatus.
func ApplyLoadBalancer(ctx context.Context, snap snap.Snap, loadbalancer types.LoadBalancer, network types.Network, annotations types.Annotations) (types.FeatureStatus, error) {
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

	if err := enableLoadBalancer(ctx, snap, loadbalancer, network, annotations); err != nil {
		err = fmt.Errorf("failed to enable LoadBalancer: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: ControllerImageTag,
			Message: fmt.Sprintf(deployFailedMsgTmpl, err),
		}, err
	}

	// Determine if annotation path is active
	annotationActive := annotations[metallbAnnotations.AnnotationBGPPeers] != ""
	bothConfigsSet := annotationActive && loadbalancer.GetBGPPeerAddress() != ""

	switch {
	case loadbalancer.GetBGPMode():
		msg := fmt.Sprintf(enabledMsgTmpl, "BGP")
		if annotationActive {
			msg = "enabled, BGP mode (alpha)"
			if bothConfigsSet {
				msg = "enabled, BGP mode (alpha) - warning: single-peer typed keys are ignored"
			}
		}
		return types.FeatureStatus{
			Enabled: true,
			Version: ControllerImageTag,
			Message: msg,
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

// buildLoadBalancerValues constructs the Helm values map for the ck-loadbalancer chart.
// neighbors is the list of BGP peers to render; advertiseAllPools controls the
// BGPAdvertisement spec (empty spec when true, named pool when false).
func buildLoadBalancerValues(lb types.LoadBalancer, neighbors []bgpNeighbor, advertiseAllPools bool) map[string]any {
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
			"peerAddress": n.peerAddress,
			"peerASN":     n.peerASN,
			"peerPort":    n.peerPort,
		}
		if n.myASN != 0 {
			nm["myASN"] = n.myASN
		}
		if len(n.nodeSelector) > 0 {
			nm["nodeSelector"] = n.nodeSelector
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

func enableLoadBalancer(ctx context.Context, snap snap.Snap, loadbalancer types.LoadBalancer, network types.Network, annotations types.Annotations) error {
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

	var (
		neighbors    []bgpNeighbor
		advertiseAll bool
	)

	annNeighbors, annAdvertiseAll, annActive, err := neighborsFromAnnotations(annotations)
	if err != nil {
		// Invalid annotation — do NOT apply broken config. Return error so ApplyLoadBalancer
		// returns a degraded FeatureStatus. The error message will be shown in k8s status.
		return fmt.Errorf("invalid BGP peer annotation: %w", err)
	}

	if annActive {
		// Annotation path: REPLACES single-peer typed keys entirely.
		neighbors = annNeighbors
		advertiseAll = annAdvertiseAll
	} else {
		// Fallback: single-peer typed keys (existing behaviour, unchanged)
		neighbors = []bgpNeighbor{{
			peerAddress: loadbalancer.GetBGPPeerAddress(),
			peerASN:     loadbalancer.GetBGPPeerASN(),
			peerPort:    loadbalancer.GetBGPPeerPort(),
		}}
		advertiseAll = false
	}

	// Validate neighbors (fail-late: this runs at reconcile time)
	if err := validateBGPNeighbors(neighbors); err != nil {
		return fmt.Errorf("invalid BGP peers: %w", err)
	}

	values := buildLoadBalancerValues(loadbalancer, neighbors, advertiseAll)

	if _, err := m.Apply(ctx, ChartMetalLBLoadBalancer, helm.StatePresent, values); err != nil {
		return fmt.Errorf("failed to apply MetalLB LoadBalancer configuration: %w", err)
	}

	return nil
}

// waitForRequiredLoadBalancerCRDs blocks until the MetalLB CRDs required for
// the current configuration are registered. The check is presence-only
// (count-based), independent of how many BGPPeer CRs will be created — so
// multi-peer configurations (including those driven by the bgp-peers annotation)
// require no changes here.
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
