package snaputil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils"
)

func IsWorker(snap snap.Snap) (bool, error) {
	return utils.FileExists(filepath.Join(snap.LockFilesDir(), "worker"))
}

func MarkAsWorkerNode(snap snap.Snap, mark bool) error {
	fname := filepath.Join(snap.LockFilesDir(), "worker")

	if mark {
		lock, err := os.Create(fname)
		if err != nil {
			return fmt.Errorf("failed to mark node as worker: %w", err)
		}
		lock.Close()
		if err := os.Chown(fname, snap.UID(), snap.GID()); err != nil {
			return fmt.Errorf("failed to chown %s: %w", fname, err)
		}
		if err := os.Chmod(fname, 0o600); err != nil {
			return fmt.Errorf("failed to chmod %s: %w", fname, err)
		}
	} else {
		if err := os.Remove(fname); err != nil {
			return fmt.Errorf("failed to unmark node as worker: %w", err)
		}
	}

	return nil
}

// KubeProxyDisabledLockFile is the node-local marker recording that kube-proxy has been stopped and its rules cleaned up.
const KubeProxyDisabledLockFile = snap.KubeProxyDisabledLockFile

// IsKubeProxyDisabled reports whether kube-proxy has been stopped and its rules cleaned up on this node.
func IsKubeProxyDisabled(snap snap.Snap) (bool, error) {
	return utils.FileExists(filepath.Join(snap.LockFilesDir(), KubeProxyDisabledLockFile))
}

// MarkKubeProxyDisabled atomically records or clears the node-local marker indicating kube-proxy is disabled and cleaned up.
func MarkKubeProxyDisabled(snap snap.Snap, disabled bool) error {
	fname := filepath.Join(snap.LockFilesDir(), KubeProxyDisabledLockFile)

	if disabled {
		tmp, err := os.CreateTemp(snap.LockFilesDir(), KubeProxyDisabledLockFile+".*")
		if err != nil {
			return fmt.Errorf("failed to create temporary kube-proxy disabled marker: %w", err)
		}
		tmpName := tmp.Name()
		cleanupTmp := true
		defer func() {
			if cleanupTmp {
				_ = os.Remove(tmpName)
			}
		}()

		if err := tmp.Close(); err != nil {
			return fmt.Errorf("failed to close %s: %w", tmpName, err)
		}
		if err := os.Chown(tmpName, snap.UID(), snap.GID()); err != nil {
			return fmt.Errorf("failed to chown %s: %w", tmpName, err)
		}
		if err := os.Chmod(tmpName, 0o600); err != nil {
			return fmt.Errorf("failed to chmod %s: %w", tmpName, err)
		}
		if err := os.Rename(tmpName, fname); err != nil {
			return fmt.Errorf("failed to rename %s to %s: %w", tmpName, fname, err)
		}
		cleanupTmp = false
	} else {
		if err := os.Remove(fname); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to clear kube-proxy disabled marker: %w", err)
		}
	}

	return nil
}
