package coredns

import (
	"path/filepath"

	"github.com/canonical/k8sd/pkg/client/helm"
)

var (
	// chartCoreDNS represents manifests to deploy CoreDNS.
	Chart = helm.InstallableChart{
		Name:         "ck-dns",
		Namespace:    "kube-system",
		ManifestPath: filepath.Join("charts", "coredns-1.47.0.tgz"),
	}

	// imageRepo is the image to use for CoreDNS.
	imageRepo = "ghcr.io/canonical/coredns"

	// ImageTag is the tag to use for the CoreDNS image.
	// TODO(Hue): need to add the 1.14.7 to the coredns-rocks repo
	ImageTag = "1.14.7-ck0"
)
