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

	// bgpBackendNative is the default BGP implementation (built-in GoBGP). Does not
	// support BFD.
	bgpBackendNative = "native"
	// bgpBackendFRRK8s is the FRR-based BGP backend (github.com/metallb/frr-k8s).
	// Required for peers that set a bfdProfile.
	bgpBackendFRRK8s = "frr-k8s"
)

// bgpNeighbor is an internal representation of a single MetalLB BGPPeer.
type bgpNeighbor struct {
	peerAddress  string
	peerASN      int
	peerPort     int
	myASN        int
	nodeSelector map[string]string
	bfdProfile   string
}

// bfdProfile is an internal representation of a single MetalLB BFDProfile.
type bfdProfile struct {
	name      string
	namespace string
	spec      map[string]any
}

// validateBGPNeighbors returns an error if any neighbor in the slice is invalid.
// frrk8sEnabled indicates whether the frr-k8s backend is active; neighbors that set
// bfdProfile are only valid when it is true, since BFD requires an FRR-based backend.
func validateBGPNeighbors(neighbors []bgpNeighbor, frrk8sEnabled bool) error {
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
		if net.ParseIP(n.peerAddress) == nil {
			return fmt.Errorf("neighbor[%d]: invalid peerAddress %q", i, n.peerAddress)
		}
		for k := range n.nodeSelector {
			if k == "" {
				return fmt.Errorf("neighbor[%d]: nodeSelector has empty key", i)
			}
		}
		if n.bfdProfile != "" && !frrk8sEnabled {
			return fmt.Errorf("neighbor[%d]: bfdProfile %q requires %s annotation set to %q", i, n.bfdProfile, metallbAnnotations.AnnotationBGPBackend, bgpBackendFRRK8s)
		}
	}
	return nil
}

// neighborsFromAnnotations parses multi-peer BGP configuration from annotations.
// It returns the neighbor slice, advertiseAllPools flag, a boolean indicating whether
// the annotation path was active, and any parse error.
// If the bgp-peers annotation is absent, returns (nil, false, inactive, nil).
//
// The annotations are typically supplied via the `annotations` key using YAML block literal syntax:
//
//	k8s set annotations="$(cat my-annotations.yaml)"
//
// where my-annotations.yaml contains:
//
//	k8sd/v1alpha1/metallb/bgp-peers: |
//	  - peerAddress: 10.0.0.1
//	    peerASN: 65001
//	    myASN: 65000
//	k8sd/v1alpha1/metallb/advertise-all-pools: "true"
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
		BFDProfile   string            `yaml:"bfdProfile"`
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
			bfdProfile:   p.BFDProfile,
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

// backendFromAnnotations parses the bgp-backend annotation and returns whether the
// frr-k8s backend should be enabled instead of the default native (GoBGP) backend.
// If the annotation is absent, empty, or set to "native", it returns (false, nil).
// If set to "frr-k8s", it returns (true, nil). Any other value is an error.
//
// The frr-k8s backend is required for BFD; see bfdProfile on individual peers in
// neighborsFromAnnotations.
func backendFromAnnotations(annotations types.Annotations) (bool, error) {
	v, ok := annotations[metallbAnnotations.AnnotationBGPBackend]
	if !ok || v == "" || v == bgpBackendNative {
		return false, nil
	}
	if v == bgpBackendFRRK8s {
		return true, nil
	}
	return false, fmt.Errorf("invalid bgp-backend annotation %q: must be %q or %q", v, bgpBackendNative, bgpBackendFRRK8s)
}

// bfdProfilesFromAnnotations parses the bfd-profiles annotation. The spec of each
// profile is passed through as-is; its fields are validated by the BFDProfile CRD.
// If the annotation is absent, returns (nil, nil).
func bfdProfilesFromAnnotations(annotations types.Annotations) ([]bfdProfile, error) {
	profilesYAML, ok := annotations[metallbAnnotations.AnnotationBFDProfiles]
	if !ok {
		return nil, nil
	}

	type profileYAML struct {
		Name      string         `yaml:"name"`
		Namespace string         `yaml:"namespace"`
		Spec      map[string]any `yaml:"spec"`
	}
	var items []profileYAML
	if err := yaml.Unmarshal([]byte(profilesYAML), &items); err != nil {
		return nil, fmt.Errorf("failed to parse bfd-profiles annotation: %w", err)
	}

	profiles := make([]bfdProfile, len(items))
	for i, p := range items {
		profiles[i] = bfdProfile{
			name:      p.Name,
			namespace: p.Namespace,
			spec:      p.Spec,
		}
	}
	return profiles, nil
}

// validateBFDProfiles returns an error if any profile is invalid. BFD profiles are
// only accepted by MetalLB with an FRR-based backend.
func validateBFDProfiles(profiles []bfdProfile, frrk8sEnabled bool) error {
	if len(profiles) > 0 && !frrk8sEnabled {
		return fmt.Errorf("%s requires %s annotation set to %q", metallbAnnotations.AnnotationBFDProfiles, metallbAnnotations.AnnotationBGPBackend, bgpBackendFRRK8s)
	}
	for i, p := range profiles {
		if p.name == "" {
			return fmt.Errorf("bfdProfile[%d]: name is required", i)
		}
	}
	return nil
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

	// Determine if annotation path is active (key present, regardless of value).
	_, annotationActive := annotations[metallbAnnotations.AnnotationBGPPeers]
	bothConfigsSet := annotationActive && loadbalancer.GetBGPPeerAddress() != ""
	// Backend validity was already enforced in enableLoadBalancer; if it were invalid
	// we would have returned above. Only the (alpha) status message cares about the
	// raw value here.
	frrk8sBackendActive := annotations[metallbAnnotations.AnnotationBGPBackend] == bgpBackendFRRK8s

	switch {
	case loadbalancer.GetBGPMode():
		msg := fmt.Sprintf(enabledMsgTmpl, "BGP")
		if annotationActive {
			msg = "enabled, BGP mode (alpha)"
			if bothConfigsSet {
				msg = "enabled, BGP mode (alpha) - warning: single-peer typed keys are ignored"
			}
		}
		if frrk8sBackendActive {
			msg += ", frr-k8s backend"
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
// BGPAdvertisement spec (empty spec when true, named pool when false); bfdProfiles
// is the list of BFDProfiles to render.
func buildLoadBalancerValues(lb types.LoadBalancer, neighbors []bgpNeighbor, advertiseAllPools bool, bfdProfiles []bfdProfile) map[string]any {
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
		if n.bfdProfile != "" {
			nm["bfdProfile"] = n.bfdProfile
		}
		neighborMaps = append(neighborMaps, nm)
	}

	// Always set (even when empty) so Helm does not coalesce stale profiles from
	// the previous release values.
	profileMaps := make([]map[string]any, 0, len(bfdProfiles))
	for _, p := range bfdProfiles {
		pm := map[string]any{"name": p.name}
		if p.namespace != "" {
			pm["namespace"] = p.namespace
		}
		if len(p.spec) > 0 {
			pm["spec"] = p.spec
		}
		profileMaps = append(profileMaps, pm)
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
			"bfdProfiles":       profileMaps,
		},
	}
}

func enableLoadBalancer(ctx context.Context, snap snap.Snap, loadbalancer types.LoadBalancer, network types.Network, annotations types.Annotations) error {
	m := snap.HelmClient()

	frrk8sEnabled, err := backendFromAnnotations(annotations)
	if err != nil {
		return fmt.Errorf("invalid BGP backend annotation: %w", err)
	}

	bfdProfiles, err := bfdProfilesFromAnnotations(annotations)
	if err != nil {
		return fmt.Errorf("invalid BFD profile annotation: %w", err)
	}
	if err := validateBFDProfiles(bfdProfiles, frrk8sEnabled); err != nil {
		return fmt.Errorf("invalid BFD profiles: %w", err)
	}

	metalLBValues := map[string]any{
		"controller": map[string]any{
			"image": map[string]any{
				"repository": controllerImageRepo,
				"tag":        ControllerImageTag,
			},
			"command": "/controller",
			// The speaker DaemonSet tolerates the control-plane taint through the
			// chart's own defaults, the controller Deployment does not. Without this
			// it stays Pending on a cluster whose control-plane nodes are tainted and
			// no untainted node has joined yet, leaving the validating webhook for
			// IPAddressPool unreachable.
			"tolerations": []map[string]any{
				{
					"key":      "node-role.kubernetes.io/control-plane",
					"operator": "Exists",
					"effect":   "NoSchedule",
				},
			},
		},
		"speaker": map[string]any{
			"image": map[string]any{
				"repository": speakerImageRepo,
				"tag":        speakerImageTag,
			},
			"command": "/speaker",
			"frr": map[string]any{
				"enabled": false,
			},
		},
		// frrk8s is a top-level value of the MetalLB chart and defaults to
		// enabled since chart 0.16.0. Controlled by the bgp-backend annotation
		// (see backendFromAnnotations); defaults to disabled (native backend).
		"frrk8s": map[string]any{
			"enabled": frrk8sEnabled,
		},
	}
	if _, err := m.Apply(ctx, ChartMetalLB, helm.StatePresent, metalLBValues); err != nil {
		return fmt.Errorf("failed to apply MetalLB configuration: %w", err)
	}

	if err := waitForRequiredLoadBalancerCRDs(ctx, snap, loadbalancer.GetBGPMode(), len(bfdProfiles) > 0); err != nil {
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
	} else if loadbalancer.GetBGPMode() {
		// Fallback: single-peer typed keys (existing behaviour, unchanged).
		// Only populated in BGP mode — L2 mode leaves neighbors empty and
		// skips BGP validation entirely.
		neighbors = []bgpNeighbor{{
			peerAddress: loadbalancer.GetBGPPeerAddress(),
			peerASN:     loadbalancer.GetBGPPeerASN(),
			peerPort:    loadbalancer.GetBGPPeerPort(),
		}}
		// advertise-all-pools annotation applies to the typed-key path too.
		if v, ok := annotations[metallbAnnotations.AnnotationAdvertiseAllPools]; ok {
			var parseErr error
			advertiseAll, parseErr = strconv.ParseBool(v)
			if parseErr != nil {
				return fmt.Errorf("failed to parse advertise-all-pools annotation %q: %w", v, parseErr)
			}
		}
	}

	// Validate BGP neighbors at reconcile time (fail-late).
	// Skipped for L2 mode where neighbors is nil.
	if err := validateBGPNeighbors(neighbors, frrk8sEnabled); err != nil {
		return fmt.Errorf("invalid BGP peers: %w", err)
	}

	values := buildLoadBalancerValues(loadbalancer, neighbors, advertiseAll, bfdProfiles)

	if _, err := m.Apply(ctx, ChartMetalLBLoadBalancer, helm.StatePresent, values); err != nil {
		return fmt.Errorf("failed to apply MetalLB LoadBalancer configuration: %w", err)
	}

	return nil
}

// waitForRequiredLoadBalancerCRDs blocks until the MetalLB CRDs required for
// the current configuration are registered. The check is presence-only
// (count-based), independent of how many BGPPeer CRs will be created — so
// multi-peer configurations (including those driven by the bgp-peers annotation)
// require no changes here. withBFD additionally requires the BFDProfile CRD.
func waitForRequiredLoadBalancerCRDs(ctx context.Context, snap snap.Snap, bgpMode bool, withBFD bool) error {
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
		if withBFD {
			requiredCRDs["metallb.io/v1beta1:bfdprofiles"] = struct{}{}
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
