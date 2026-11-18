package cilium

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/sys/mountinfo"
	"golang.org/x/sys/unix"
)

type mountIdentity struct {
	dev      uint64
	mntID    uint64
	hasMntID bool
}

func sameMount(a, b mountIdentity) bool {
	if a.dev != b.dev {
		return false
	}
	if a.hasMntID && b.hasMntID && a.mntID != b.mntID {
		return false
	}
	return true
}

func statPathMountIdentity(path string) (mountIdentity, error) {
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &stx); err != nil {
		return mountIdentity{}, err
	}
	return mountIdentity{
		dev:      (uint64(stx.Dev_major) << 32) | uint64(stx.Dev_minor),
		mntID:    stx.Mnt_id,
		hasMntID: stx.Mask&unix.STATX_MNT_ID != 0,
	}, nil
}

func statFileMountIdentity(f *os.File) (mountIdentity, error) {
	var stx unix.Statx_t
	if err := unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &stx); err != nil {
		return mountIdentity{}, err
	}
	return mountIdentity{
		dev:      (uint64(stx.Dev_major) << 32) | uint64(stx.Dev_minor),
		mntID:    stx.Mnt_id,
		hasMntID: stx.Mask&unix.STATX_MNT_ID != 0,
	}, nil
}

func removeCiliumDirectories(ctx context.Context, paths []string, readMounts func(mountinfo.FilterFunc) ([]*mountinfo.Info, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var existing []string
	var errs []error
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("stat Cilium directory %s: %w", path, err))
			continue
		}
		existing = append(existing, path)
	}
	if len(existing) == 0 {
		return errors.Join(errs...)
	}
	for _, path := range existing {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := removeCiliumDirectoryWith(ctx, path, readMounts, statFileMountIdentity); err != nil {
			errs = append(errs, fmt.Errorf("remove Cilium directory %s: %w", path, err))
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(errs...)
}

func removeCiliumDirectory(ctx context.Context, path string, mounts []*mountinfo.Info) error {
	return removeCiliumDirectoryWith(
		ctx,
		path,
		func(mountinfo.FilterFunc) ([]*mountinfo.Info, error) { return mounts, nil },
		statFileMountIdentity,
	)
}

func checkMountsUnder(mounts []*mountinfo.Info, cleaned, resolved string) error {
	for _, mount := range mounts {
		mounted := filepath.Clean(mount.Mountpoint)
		if mounted == cleaned || strings.HasPrefix(mounted, cleaned+string(filepath.Separator)) ||
			mounted == resolved || strings.HasPrefix(mounted, resolved+string(filepath.Separator)) {
			return fmt.Errorf("refusing mounted Cilium state at %s", mounted)
		}
	}
	return nil
}

func removeCiliumDirectoryWith(
	ctx context.Context,
	path string,
	readMounts func(mountinfo.FilterFunc) ([]*mountinfo.Info, error),
	statMount func(*os.File) (mountIdentity, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing non-directory or symlink root")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	cleaned := filepath.Clean(path)
	parentMnt, err := statPathMountIdentity(filepath.Dir(cleaned))
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()

	rootDir, err := root.Open(".")
	if err != nil {
		return err
	}
	opened, statErr := rootDir.Stat()
	rootMnt, mntErr := statMount(rootDir)
	closeErr := rootDir.Close()
	if err := errors.Join(statErr, mntErr, closeErr); err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("Cilium directory changed before cleanup")
	}
	if !sameMount(parentMnt, rootMnt) {
		return fmt.Errorf("refusing mounted Cilium state at %s", cleaned)
	}

	if readMounts != nil {
		mounts, err := readMounts(nil)
		if err != nil {
			return fmt.Errorf("read mounts before removing Cilium directory: %w", err)
		}
		if err := checkMountsUnder(mounts, cleaned, resolved); err != nil {
			return err
		}
	}

	if err := removeCiliumDirectoryContents(ctx, root, rootMnt, statMount); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !os.SameFile(info, current) {
		return fmt.Errorf("Cilium directory changed during cleanup")
	}
	curMnt, err := statPathMountIdentity(cleaned)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !sameMount(rootMnt, curMnt) {
		return fmt.Errorf("refusing mounted Cilium state at %s", cleaned)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeCiliumDirectoryContents(
	ctx context.Context,
	root *os.Root,
	rootMnt mountIdentity,
	statMount func(*os.File) (mountIdentity, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	dirMnt, mntErr := statMount(dir)
	if mntErr != nil {
		_ = dir.Close()
		return mntErr
	}
	if !sameMount(rootMnt, dirMnt) {
		_ = dir.Close()
		return fmt.Errorf("refusing mounted Cilium state subtree")
	}
	entries, readErr := dir.ReadDir(-1)
	if err := errors.Join(readErr, dir.Close()); err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		name := entry.Name()
		info, statErr := root.Lstat(name)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			errs = append(errs, statErr)
			continue
		}
		if info.IsDir() {
			sub, openErr := root.OpenRoot(name)
			if openErr != nil {
				if !errors.Is(openErr, os.ErrNotExist) {
					errs = append(errs, openErr)
				}
				continue
			}
			subErr := removeCiliumDirectoryContents(ctx, sub, rootMnt, statMount)
			closeErr := sub.Close()
			if err := errors.Join(subErr, closeErr); err != nil {
				errs = append(errs, err)
				if ctx.Err() != nil {
					break
				}
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if rmErr := root.Remove(name); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			errs = append(errs, rmErr)
		}
	}
	return errors.Join(errs...)
}
