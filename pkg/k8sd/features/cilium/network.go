package cilium

import (
	"context"
	"fmt"
	"net"
	"strings"

	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations/cilium"
	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/canonical/k8sd/pkg/utils/control"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

const (
	NetworkDeleteFailedMsgTmpl = "Failed to delete Cilium Network, the error was: %v"
	NetworkDeployFailedMsgTmpl = "Failed to deploy Cilium Network, the error was: %v"
	// NetworkDevicesWarningMsgTmpl is reported when the configured Cilium devices do
	// not cover the device that carries the node's default route. Cilium reverse-NATs
	// the reply of a LoadBalancer/NodePort connection that is served by a pod on the
	// ingress node in its egress program. The kernel routes that reply with the pod IP
	// as source, so with source based policy routing it can leave through the default
	// route device. If Cilium does not manage that device, the reply escapes the node
	// untranslated and the client never sees it.
	NetworkDevicesWarningMsgTmpl = "enabled. Warning: the %q annotation (%q) does not match the default route device %q. Reply traffic of LoadBalancer/NodePort connections served by a pod on this node may leave %q without reverse NAT. Add it to the annotation."
)

// required for unittests.
var (
	GetMountPath            = utils.GetMountPath
	GetMountPropagationType = utils.GetMountPropagationType
	GetDefaultRouteDevice   = utils.GetDefaultRouteDevice
)

// Cilium uses vxlan encapsulation protocol by default. Since we are using the default
// cilium tunnel encapsulation protocol, we have to make sure that Cilium's vxlan is the
// only interface using the default vxlan port. Otherwise, Cilium might conflict with
// other tools such as fan-netwotking, which use the same vxlan destination port.
func checkAndSanitizeCiliumVXLAN(port int) error {
	vxlanDevices, err := utils.ListVXLANInterfaces()
	if err != nil {
		return fmt.Errorf("listing vxlan interfaces failed: %w", err)
	}

	for _, vxlanDevice := range vxlanDevices {
		if vxlanDevice.Port == nil {
			// This vxlan interface does not have a port set
			// or it was not included in the output of `ip -d -j link list type vxlan`.
			continue
		}

		devicePort := *vxlanDevice.Port

		if devicePort == port && vxlanDevice.Name != ciliumVXLANDeviceName {
			return fmt.Errorf("interface %s uses the same destination port as cilium. Please consider changing the Cilium tunnel port", vxlanDevice.Name)
		}
	}

	return nil
}

// ApplyNetwork will deploy Cilium when network.Enabled is true.
// ApplyNetwork will remove Cilium when network.Enabled is false.
// ApplyNetwork requires that bpf and cgroups2 are already mounted and available when running under strict snap confinement. If they are not, it will fail (since Cilium will not have the required permissions to mount them).
// ApplyNetwork requires that `/sys` is mounted as a shared mount when running under classic snap confinement. This is to ensure that Cilium will be able to automatically mount bpf and cgroups2 on the pods.
// ApplyNetwork will always return a FeatureStatus indicating the current status of the
// deployment.
// ApplyNetwork returns an error if anything fails. The error is also wrapped in the .Message field of the
// returned FeatureStatus.
func ApplyNetwork(ctx context.Context, snap snap.Snap, s mctypes.State, apiserver types.APIServer, network types.Network, annotations types.Annotations) (types.FeatureStatus, error) {
	m := snap.HelmClient()

	if !network.GetEnabled() {
		if _, err := m.Apply(ctx, ChartCilium, helm.StateDeleted, nil); err != nil {
			err = fmt.Errorf("failed to uninstall network: %w", err)
			return types.FeatureStatus{
				Enabled: false,
				Version: CiliumAgentImageTag,
				Message: fmt.Sprintf(NetworkDeleteFailedMsgTmpl, err),
			}, err
		}
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: DisabledMsg,
		}, nil
	}

	config, err := internalConfig(annotations)
	if err != nil {
		err = fmt.Errorf("failed to parse annotations: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
		}, err
	}

	// Cilium only reverse-NATs and re-routes the reply of a LoadBalancer/NodePort
	// connection on devices it manages, so the device carrying the default route must
	// be part of the filter (that is where the kernel sends those replies).
	devicesWarning := checkDevicesCoverDefaultRoute(ctx, config.devices)

	localhostAddress, err := utils.GetLocalhostAddress()
	if err != nil {
		err = fmt.Errorf("failed to determine localhost address: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
		}, err
	}

	nodeIP := net.ParseIP(s.Address().Hostname())
	if nodeIP == nil {
		err = fmt.Errorf("failed to parse node IP address %q", s.Address().Hostname())
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
		}, err
	}

	ipv4CIDR, ipv6CIDR, err := utils.SplitCIDRStrings(network.GetPodCIDR())
	if err != nil {
		err = fmt.Errorf("invalid kube-proxy --cluster-cidr value: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
		}, err
	}

	ciliumNodePortValues := map[string]any{
		"enabled": true,
		// With kube-proxy replacement enabled, we can safely enable the health check
		// since kube-proxy won't be running and won't conflict with the health check port
		"enableHealthCheck": true,
	}

	if config.directRoutingDevice != "" {
		ciliumNodePortValues["directRoutingDevice"] = config.directRoutingDevice
	}

	if err := checkAndSanitizeCiliumVXLAN(config.tunnelPort); err != nil {
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
		}, err
	}

	bpfValues := map[string]any{}
	if config.vlanBPFBypass != nil {
		bpfValues["vlanBypass"] = config.vlanBPFBypass
	}

	values := map[string]any{
		"bpf": bpfValues,
		"image": map[string]any{
			"repository": ciliumAgentImageRepo,
			"tag":        CiliumAgentImageTag,
			"useDigest":  false,
		},
		"socketLB": map[string]any{
			"enabled": true,
		},
		"cni": map[string]any{
			"confPath":     "/etc/cni/net.d",
			"binPath":      "/opt/cni/bin",
			"exclusive":    config.cniExclusive,
			"chainingMode": "portmap",
		},
		"sctp": map[string]any{
			"enabled": config.sctpEnabled,
		},
		"operator": map[string]any{
			"replicas": 1,
			"image": map[string]any{
				"repository": ciliumOperatorImageRepo,
				"tag":        ciliumOperatorImageTag,
				"useDigest":  false,
			},
		},
		"ipv4": map[string]any{
			"enabled": ipv4CIDR != "",
		},
		"ipv6": map[string]any{
			"enabled": ipv6CIDR != "",
		},
		"ipam": map[string]any{
			"operator": map[string]any{
				"clusterPoolIPv4PodCIDRList": ipv4CIDR,
				"clusterPoolIPv6PodCIDRList": ipv6CIDR,
			},
		},
		"envoy": map[string]any{
			"enabled": false, // 1.16+ installs envoy as a standalone daemonset by default if not explicitly disabled
		},
		// Enable kube-proxy replacement mode so that Cilium handles all kube-proxy functionality
		// https://docs.cilium.io/en/v1.15/network/kubernetes/kubeproxy-free/
		"kubeProxyReplacement":     true,
		"nodePort":                 ciliumNodePortValues,
		"disableEnvoyVersionCheck": true,
		// socketLB requires an endpoint to the apiserver that's not managed by the kube-proxy
		// so we point to the localhost:secureport to talk to either the kube-apiserver or the kube-apiserver-proxy
		"k8sServiceHost": strings.Trim(localhostAddress.String(), "[]"), // Cilium already adds the brackets for ipv6 addresses, so we need to remove them
		"k8sServicePort": apiserver.GetSecurePort(),
		// This flag enables the runtime device detection which is set to true by default in Cilium 1.16+
		"enableRuntimeDeviceDetection": true,
		"sessionAffinity":              true,
		"loadBalancer": map[string]any{
			"protocolDifferentiation": map[string]any{
				"enabled": true,
			},
		},
		"tunnelPort": config.tunnelPort,
	}

	// Revert these values to default in case they were changed in previous versions
	if ipv4CIDR == "" && ipv6CIDR != "" {
		values["routingMode"] = "tunnel"
		values["ipv6NativeRoutingCIDR"] = ""
		values["autoDirectNodeRoutes"] = false
	}

	if config.devices != "" {
		values["devices"] = config.devices
	}

	if snap.Strict() {
		bpfMnt, err := GetMountPath("bpf")
		if err != nil {
			err = fmt.Errorf("failed to get bpf mount path: %w", err)
			return types.FeatureStatus{
				Enabled: false,
				Version: CiliumAgentImageTag,
				Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
			}, err
		}

		cgrMnt, err := GetMountPath("cgroup2")
		if err != nil {
			err = fmt.Errorf("failed to get cgroup2 mount path: %w", err)
			return types.FeatureStatus{
				Enabled: false,
				Version: CiliumAgentImageTag,
				Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
			}, err
		}

		values["bpf"] = map[string]any{
			"autoMount": map[string]any{
				"enabled": false,
			},
			"root": bpfMnt,
		}
		values["cgroup"] = map[string]any{
			"autoMount": map[string]any{
				"enabled": false,
			},
			"hostRoot": cgrMnt,
		}
	} else {
		pt, err := GetMountPropagationType("/sys")
		if err != nil {
			err = fmt.Errorf("failed to get mount propagation type for /sys: %w", err)
			return types.FeatureStatus{
				Enabled: false,
				Version: CiliumAgentImageTag,
				Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
			}, err
		}
		if pt == utils.MountPropagationPrivate {
			onLXD, err := snap.OnLXD(ctx)
			if err != nil {
				logger := log.FromContext(ctx)
				logger.Error(err, "Failed to check if running on LXD")
			}
			if onLXD {
				err := fmt.Errorf("/sys is not a shared mount on the LXD container, this might be resolved by updating LXD on the host to version 5.0.2 or newer")
				return types.FeatureStatus{
					Enabled: false,
					Version: CiliumAgentImageTag,
					Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
				}, err
			}

			err = fmt.Errorf("/sys is not a shared mount")
			return types.FeatureStatus{
				Enabled: false,
				Version: CiliumAgentImageTag,
				Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
			}, err
		}
	}

	if _, err := m.Apply(ctx, ChartCilium, helm.StatePresent, values); err != nil {
		err = fmt.Errorf("failed to enable network: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
		}, err
	}

	// TODO(Hue): we should only rollout restart if necessary.
	if err := rolloutRestartCilium(ctx, snap, 3); err != nil {
		err = fmt.Errorf("failed to rollout restart cilium to apply new network configuration: %w", err)
		return types.FeatureStatus{
			Enabled: false,
			Version: CiliumAgentImageTag,
			Message: fmt.Sprintf(NetworkDeployFailedMsgTmpl, err),
		}, err
	}

	message := EnabledMsg
	if devicesWarning != "" {
		message = devicesWarning
	}

	return types.FeatureStatus{
		Enabled: true,
		Version: CiliumAgentImageTag,
		Message: message,
	}, nil
}

// checkDevicesCoverDefaultRoute returns a warning message if the given Cilium
// `devices` filter is set but does not select the device that carries the node's
// default route. It returns an empty string when the filter is unset (Cilium then
// detects the devices itself and always includes the default route device), when the
// filter covers that device, or when the device cannot be determined.
func checkDevicesCoverDefaultRoute(ctx context.Context, devices string) string {
	if devices == "" {
		return ""
	}

	logger := log.FromContext(ctx)

	defaultRouteDevice, err := GetDefaultRouteDevice()
	if err != nil {
		logger.Error(err, "Failed to determine the default route device, skipping Cilium devices check")
		return ""
	}

	if matchesDeviceFilter(devices, defaultRouteDevice) {
		return ""
	}

	message := fmt.Sprintf(NetworkDevicesWarningMsgTmpl, apiv1_annotations.AnnotationDevices, devices, defaultRouteDevice, defaultRouteDevice)
	logger.Info(message, "devices", devices, "defaultRouteDevice", defaultRouteDevice)

	return message
}

func rolloutRestartCilium(ctx context.Context, snap snap.Snap, attempts int) error {
	client, err := snap.KubernetesClient("")
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	if err := control.RetryFor(ctx, attempts, 0, func() error {
		if err := client.RestartDeployment(ctx, "cilium-operator", "kube-system"); err != nil {
			return fmt.Errorf("failed to restart cilium-operator deployment: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to restart cilium-operator deployment after %d attempts: %w", attempts, err)
	}

	if err := control.RetryFor(ctx, attempts, 0, func() error {
		if err := client.RestartDaemonset(ctx, "cilium", "kube-system"); err != nil {
			return fmt.Errorf("failed to restart cilium daemonset: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to restart cilium daemonset after %d attempts: %w", attempts, err)
	}

	return nil
}
