package localpv

import (
	"path/filepath"

	"github.com/canonical/k8sd/pkg/client/helm"
)

var (
	// Chart represents manifests to deploy Rawfile LocalPV CSI.
	Chart = helm.InstallableChart{
		Name:         "ck-storage",
		Namespace:    "kube-system",
		ManifestPath: filepath.Join("charts", "rawfile-csi-0.9.2.tgz"),
	}

	// imageRepo is the repository to use for Rawfile LocalPV CSI.
	imageRepo = "ghcr.io/canonical/rawfile-localpv"
	// ImageTag is the image tag to use for Rawfile LocalPV CSI.
	ImageTag = "0.8.3-ck4"

	// csiNodeDriverImage is the image to use for the CSI node driver.
	csiNodeDriverImage = "ghcr.io/canonical/csi-node-driver-registrar:2.17.0-ck0"
	// csiProvisionerImage is the image to use for the CSI provisioner.
	csiProvisionerImage = "ghcr.io/canonical/csi-provisioner:6.3.0-ck0"
	// csiResizerImage is the image to use for the CSI resizer.
	csiResizerImage = "ghcr.io/canonical/csi-resizer:2.2.1-ck0"
	// csiSnapshotterImage is the image to use for the CSI snapshotter.
	csiSnapshotterImage = "ghcr.io/canonical/csi-snapshotter:8.6.0-ck0"
)
