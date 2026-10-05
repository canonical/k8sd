package cilium

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations/cilium"
	"github.com/canonical/k8sd/pkg/k8sd/types"
)

const (
	// minVLANIDValue is the minimum valid 802.1Q VLAN ID value.
	minVLANIDValue = 0
	// maxVLANIDValue is the maximum valid 802.1Q VLAN ID value.
	maxVLANIDValue = 4094
	// minClusterIDValue is the minimum valid Cilium cluster ID value.
	minClusterIDValue = 0
	// maxClusterIDValue is the maximum valid Cilium cluster ID value.
	maxClusterIDValue = 255
)

type config struct {
	devices             string
	directRoutingDevice string
	vlanBPFBypass       []int
	cniExclusive        bool
	sctpEnabled         bool
	tunnelPort          int
	clusterID           int
	clusterName         string
}

func validatePort(portStr string) (int, error) {
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, errors.New("invalid port: not a number")
	}
	if port < 1 || port > 65535 {
		return 0, errors.New("invalid port: out of range")
	}
	return port, nil
}

func validateClusterID(clusterIDStr string) (int, error) {
	clusterID, err := strconv.Atoi(clusterIDStr)
	if err != nil {
		return 0, errors.New("invalid cluster ID: not a number")
	}
	if clusterID < minClusterIDValue || clusterID > maxClusterIDValue {
		return 0, fmt.Errorf("invalid cluster ID: must be between %d and %d", minClusterIDValue, maxClusterIDValue)
	}
	return clusterID, nil
}

// validateClusterName checks the cluster name against the constraints defined
// by the Cilium chart: it must contain at most 32 characters, begin and end
// with a lower case alphanumeric character and may only contain lower case
// alphanumeric characters and dashes between.
func validateClusterName(clusterName string) error {
	if clusterName == "" {
		return errors.New("invalid cluster name: must not be empty")
	}
	if len(clusterName) > 32 {
		return errors.New("invalid cluster name: must contain at most 32 characters")
	}
	for i, r := range clusterName {
		isLowerAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !isLowerAlnum && r != '-' {
			return fmt.Errorf("invalid cluster name: character %q is not a lower case alphanumeric character or a dash", r)
		}
		if r == '-' && (i == 0 || i == len(clusterName)-1) {
			return errors.New("invalid cluster name: must begin and end with a lower case alphanumeric character")
		}
	}
	return nil
}

func validateVLANBPFBypass(vlanList string) ([]int, error) {
	vlanList = strings.TrimSpace(vlanList)
	// Maintain compatibility with the Cilium chart definition
	vlanList = strings.Trim(vlanList, "{}")
	vlans := strings.Split(vlanList, ",")

	vlanTags := make([]int, 0, len(vlans))
	seenTags := make(map[int]struct{})

	for _, vlan := range vlans {
		vlanID, err := strconv.Atoi(strings.TrimSpace(vlan))
		if err != nil {
			return []int{}, fmt.Errorf("failed to parse VLAN tag: %w", err)
		}
		if vlanID < minVLANIDValue || vlanID > maxVLANIDValue {
			return []int{}, fmt.Errorf("VLAN tag must be between 0 and %d", maxVLANIDValue)
		}

		if _, ok := seenTags[vlanID]; ok {
			continue
		}
		seenTags[vlanID] = struct{}{}
		vlanTags = append(vlanTags, vlanID)
	}

	slices.Sort(vlanTags)
	return vlanTags, nil
}

func internalConfig(annotations types.Annotations) (config, error) {
	c := config{}

	if v, ok := annotations.Get(apiv1_annotations.AnnotationDevices); ok {
		c.devices = v
	}

	if v, ok := annotations.Get(apiv1_annotations.AnnotationDirectRoutingDevice); ok {
		c.directRoutingDevice = v
	}

	if v, ok := annotations[apiv1_annotations.AnnotationVLANBPFBypass]; ok {
		vlanTags, err := validateVLANBPFBypass(v)
		if err != nil {
			return config{}, fmt.Errorf("failed to parse VLAN BPF bypass list: %w", err)
		}
		c.vlanBPFBypass = vlanTags
	}

	if _, ok := annotations.Get(apiv1_annotations.AnnotationCNIExclusive); ok {
		c.cniExclusive = true
	}

	if _, ok := annotations.Get(apiv1_annotations.AnnotationSCTPEnabled); ok {
		c.sctpEnabled = true
	}

	if v, ok := annotations.Get(apiv1_annotations.AnnotationTunnelPort); ok {
		tunnelPort, err := validatePort(v)
		if err != nil {
			return config{}, fmt.Errorf("failed to parse Tunnel encapsulation port: %w", err)
		}

		c.tunnelPort = tunnelPort
	} else {
		c.tunnelPort = ciliumDefaultVXLANPort
	}

	if v, ok := annotations.Get(apiv1_annotations.AnnotationClusterID); ok {
		clusterID, err := validateClusterID(v)
		if err != nil {
			return config{}, fmt.Errorf("failed to parse cluster ID: %w", err)
		}

		c.clusterID = clusterID
	}

	if v, ok := annotations.Get(apiv1_annotations.AnnotationClusterName); ok {
		if err := validateClusterName(v); err != nil {
			return config{}, fmt.Errorf("failed to parse cluster name: %w", err)
		}

		c.clusterName = v
	}

	// The Cilium chart does not allow the name "default" with a non-zero
	// cluster ID.
	if c.clusterID != 0 && (c.clusterName == "" || c.clusterName == ciliumDefaultClusterName) {
		return config{}, fmt.Errorf("cluster name %q cannot be used with a non-zero cluster ID: set the %q annotation", ciliumDefaultClusterName, apiv1_annotations.AnnotationClusterName)
	}

	return c, nil
}
