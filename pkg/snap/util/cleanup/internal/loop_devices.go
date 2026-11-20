package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/k8sd/pkg/log"
	mountutils "github.com/canonical/k8sd/pkg/utils/mount"
)

func isKubeletPodMount(mountPoint string) bool {
	if hasMountPrefix(mountPoint, "/var/lib/kubelet/pods") {
		return true
	}
	if snapCommon := os.Getenv("SNAP_COMMON"); snapCommon != "" {
		return hasMountPrefix(mountPoint, filepath.Join(snapCommon, "var/lib/kubelet/pods"))
	}
	return false
}

func RemoveLoopDevices(ctx context.Context, mountHelper mountutils.MountManager) {
	log := log.FromContext(ctx)

	err := mountHelper.ForEachMount(ctx, func(ctx context.Context, device string, mountPoint string, fsType string, flags string) error {
		if isKubeletPodMount(mountPoint) && strings.HasPrefix(device, "/dev/loop") {
			// Gather loop devices for detachment
			return mountHelper.DetachLoopDevice(ctx, device)
		}

		return nil
	})
	if err != nil {
		log.Error(err, "failed to iterate mounts for loop devices")
	}
}
