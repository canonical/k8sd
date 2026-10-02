package internal

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
	mountutils "github.com/canonical/k8sd/pkg/utils/mount"
	"golang.org/x/sys/unix"
)

// iscsiadmNoObjectsFound is the exit code returned by iscsiadm when no iSCSI sessions exist.
const iscsiadmNoObjectsFound = 21

// GetISCSISessionsToLogout finds the iSCSI session IDs (SIDs) that correspond to block devices
// currently mounted under Kubernetes-managed directories.
func GetISCSISessionsToLogout(ctx context.Context, s snap.Snap, mountHelper mountutils.MountManager) []string {
	log := log.FromContext(ctx)
	var sids []string

	if _, err := exec.LookPath("iscsiadm"); err != nil {
		return sids
	}

	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

	defer cancel()
	out, err := exec.CommandContext(queryCtx, "iscsiadm", "-m", "session", "-P", "3").CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == iscsiadmNoObjectsFound {
			return sids
		}
		log.Error(err, "failed to get iscsi sessions", "output", string(out))
		return sids
	}

	devToSID := make(map[string]string)
	var currentSID string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SID:") {
			parts := strings.Split(line, "SID:")
			if len(parts) > 1 {
				currentSID = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(line, "Attached scsi disk") && currentSID != "" {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				dev := "/dev/" + parts[3]
				devToSID[dev] = currentSID
			}
		}
	}

	if len(devToSID) == 0 {
		return sids
	}

	prefixes := append([]string{"/var/lib/kubelet/pods", "/var/lib/kubelet/plugins"}, containerdMountPrefixes(ctx, s)...)
	sidSet := make(map[string]bool)

	err = mountHelper.ForEachMount(ctx, func(ctx context.Context, device string, mountPoint string, fsType string, flags string) error {
		for _, prefix := range prefixes {
			if hasMountPrefix(mountPoint, prefix) {
				resolvedDev, err := filepath.EvalSymlinks(device)
				if err != nil {
					resolvedDev = device
				}
				if sid, ok := devToSID[resolvedDev]; ok {
					sidSet[sid] = true
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		log.Error(err, "failed to iterate mounts to find iSCSI sessions")
	}

	for sid := range sidSet {
		sids = append(sids, sid)
	}
	return sids
}

// LogoutISCSISessions logs out specific iSCSI sessions, allowing volume unmounts to proceed
// without blocking on the kernel's iSCSI session recovery timeout.
func LogoutISCSISessions(ctx context.Context, sids []string) {
	log := log.FromContext(ctx)

	if len(sids) == 0 {
		return
	}

	if _, err := exec.LookPath("iscsiadm"); err != nil {
		log.Info("iscsiadm not found, skipping iSCSI session logout")
		return
	}

	for _, sid := range sids {
		log.Info("Logging out iSCSI session", "sid", sid)
		logoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

		out, err := exec.CommandContext(logoutCtx, "iscsiadm", "-m", "session", "-r", sid, "-u").CombinedOutput()
		cancel()
		if err != nil {
			log.Error(err, "failed to logout iSCSI session", "sid", sid, "output", string(out))
		}
	}
}

// SyncISCSIDevices flushes all pending I/O to iSCSI-backed block devices.
func SyncISCSIDevices(ctx context.Context) {
	log := log.FromContext(ctx)

	unix.Sync()

	if _, err := exec.LookPath("iscsiadm"); err != nil {
		return
	}

	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

	defer cancel()
	out, err := exec.CommandContext(queryCtx, "iscsiadm", "-m", "session", "-P", "3").CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != iscsiadmNoObjectsFound {
			log.Error(err, "failed to query iscsi sessions for sync", "output", string(out))
		}
		return
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Attached scsi disk") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		dev := "/dev/" + fields[3]

		log.Info("Flushing iSCSI block device buffer", "device", dev)
		flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

		flushOut, err := exec.CommandContext(flushCtx, "blockdev", "--flushbufs", dev).CombinedOutput()
		cancel()
		if err != nil {
			log.Error(err, "Failed to flush iSCSI block device buffers", "device", dev, "output", string(flushOut))
		}
	}
}
