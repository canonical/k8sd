package mountutils

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type UnixMountManager struct{}

func (h UnixMountManager) ForEachMount(ctx context.Context, callback func(ctx context.Context, device string, mountPoint string, fsType string, flags string) error) error {
	return forEachMount(ctx, "/proc/mounts", callback)
}

func (h UnixMountManager) Unmount(ctx context.Context, mountPoint string, flags int) error {
	if err := unix.Unmount(mountPoint, flags); err != nil {
		return fmt.Errorf("failed to unmount %s: %w", mountPoint, err)
	}
	return nil
}

func (h UnixMountManager) DetachLoopDevice(ctx context.Context, device string) error {
	f, err := os.Open(device)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to open loop device %s: %w", device, err)
	}
	defer f.Close()

	if err := unix.IoctlSetInt(int(f.Fd()), unix.LOOP_CLR_FD, 0); err != nil && !errors.Is(err, unix.ENXIO) {
		return fmt.Errorf("failed to detach loop device %s: %w", device, err)
	}
	return nil
}

func NewUnixMountHelper() MountManager {
	return &UnixMountManager{}
}
