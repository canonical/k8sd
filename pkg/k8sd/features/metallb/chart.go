package metallb

import (
	"path/filepath"

	"github.com/canonical/k8sd/pkg/client/helm"
)

var (
	// ChartMetalLB represents manifests to deploy MetalLB speaker and controller.
	ChartMetalLB = helm.InstallableChart{
		Name:         "metallb",
		Namespace:    "metallb-system",
		ManifestPath: filepath.Join("charts", "metallb-0.16.1.tgz"),
	}

	// ChartMetalLBLoadBalancer represents manifests to deploy MetalLB L2 or BGP resources.
	ChartMetalLBLoadBalancer = helm.InstallableChart{
		Name:         "metallb-loadbalancer",
		Namespace:    "metallb-system",
		ManifestPath: filepath.Join("charts", "ck-loadbalancer"),
	}

	// controllerImageRepo is the image to use for metallb-controller.
	controllerImageRepo = "ghcr.io/canonical/metallb-controller"

	// ControllerImageTag is the tag to use for metallb-controller.
	ControllerImageTag = "v0.16.1-ck0"

	// speakerImageRepo is the image to use for metallb-speaker.
	speakerImageRepo = "ghcr.io/canonical/metallb-speaker"

	// speakerImageTag is the tag to use for metallb-speaker.
	speakerImageTag = "v0.16.1-ck0"

	// frrk8sImageRepo is the image to use for frr-k8s.
	frrk8sImageRepo = "ghcr.io/canonical/frr-k8s"

	// frrk8sImageTag is the tag to use for frr-k8s.
	frrk8sImageTag = "v0.0.25-ck0"

	// frrImageRepo is the image to use for the FRR containers of frr-k8s.
	frrImageRepo = "ghcr.io/canonical/frr"

	// frrImageTag is the tag to use for the FRR containers of frr-k8s.
	frrImageTag = "10.4.3-ck0"
)
