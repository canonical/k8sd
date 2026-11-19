package snaputil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/k8sd/pkg/snap/mock"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	. "github.com/onsi/gomega"
)

func TestIsWorker(t *testing.T) {
	mock := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: t.TempDir(),
		},
	}

	t.Run("WorkerFileExists", func(t *testing.T) {
		g := NewWithT(t)

		fname := filepath.Join(mock.LockFilesDir(), "worker")
		lock, err := os.Create(fname)
		g.Expect(err).ToNot(HaveOccurred())
		lock.Close()

		exists, err := snaputil.IsWorker(mock)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(exists).To(BeTrue())
	})

	t.Run("WorkerFileNotExists", func(t *testing.T) {
		mock.Mock.LockFilesDir = "/non-existent"
		g := NewWithT(t)
		exists, err := snaputil.IsWorker(mock)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(exists).To(BeFalse())
	})
}

func TestMarkAsWorkerNode(t *testing.T) {
	lockFilesDir := t.TempDir()
	mock := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: lockFilesDir,
			UID:          -1,
			GID:          -1,
		},
	}

	t.Run("MarkWorker", func(t *testing.T) {
		g := NewWithT(t)
		err := snaputil.MarkAsWorkerNode(mock, true)
		g.Expect(err).To(Not(HaveOccurred()))

		workerFile := filepath.Join(mock.LockFilesDir(), "worker")
		g.Expect(workerFile).To(BeAnExistingFile())

		// Clean up
		err = os.Remove(workerFile)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("UnmarkWorker", func(t *testing.T) {
		g := NewWithT(t)
		workerFile := filepath.Join(mock.LockFilesDir(), "worker")
		_, err := os.Create(workerFile)
		g.Expect(err).To(Not(HaveOccurred()))

		err = snaputil.MarkAsWorkerNode(mock, false)
		g.Expect(err).To(Not(HaveOccurred()))

		g.Expect(workerFile).NotTo(BeAnExistingFile())
	})
}

func TestMarkAsWorkerNode_ErrorCases(t *testing.T) {
	mock := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: t.TempDir(),
			UID:          1000,
			GID:          1000,
		},
	}

	t.Run("FailedToCreateWorkerFile", func(t *testing.T) {
		mock.Mock.LockFilesDir = "/non-existent"
		g := NewWithT(t)
		err := snaputil.MarkAsWorkerNode(mock, true)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("FailedToRemoveWorkerFile", func(t *testing.T) {
		mock.Mock.LockFilesDir = "/non-existent"
		g := NewWithT(t)
		err := snaputil.MarkAsWorkerNode(mock, false)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("FailedToChownWorkerFile", func(t *testing.T) {
		mock.Mock.UID = -1 // Invalid UID to cause chown failure
		g := NewWithT(t)
		err := snaputil.MarkAsWorkerNode(mock, true)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("FailedToChmodWorkerFile", func(t *testing.T) {
		mock.Mock.LockFilesDir = "/non-existent"
		g := NewWithT(t)
		err := snaputil.MarkAsWorkerNode(mock, true)
		g.Expect(err).To(HaveOccurred())
	})
}

func TestMarkKubeProxyDisabled(t *testing.T) {
	t.Run("SetAndClearMarker", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		s := &mock.Snap{
			Mock: mock.Mock{
				LockFilesDir: dir,
				UID:          os.Getuid(),
				GID:          os.Getgid(),
			},
		}

		disabled, err := snaputil.IsKubeProxyDisabled(s)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(disabled).To(BeFalse())

		g.Expect(snaputil.MarkKubeProxyDisabled(s, true)).To(Succeed())
		disabled, err = snaputil.IsKubeProxyDisabled(s)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(disabled).To(BeTrue())

		g.Expect(snaputil.MarkKubeProxyDisabled(s, false)).To(Succeed())
		disabled, err = snaputil.IsKubeProxyDisabled(s)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(disabled).To(BeFalse())

		// Clearing an already absent marker is a no-op.
		g.Expect(snaputil.MarkKubeProxyDisabled(s, false)).To(Succeed())
	})

	t.Run("FailedRenameCleansUpTempAndLeavesMarkerAbsent", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		// Create a non-empty directory at the target marker path so os.Rename fails even when running as root.
		targetDir := filepath.Join(dir, snaputil.KubeProxyDisabledLockFile)
		g.Expect(os.MkdirAll(filepath.Join(targetDir, "nested"), 0o755)).To(Succeed())

		s := &mock.Snap{
			Mock: mock.Mock{
				LockFilesDir: dir,
				UID:          os.Getuid(),
				GID:          os.Getgid(),
			},
		}

		err := snaputil.MarkKubeProxyDisabled(s, true)
		g.Expect(err).To(HaveOccurred())

		// Ensure no temporary kube-proxy-disabled.* files remain behind.
		entries, readErr := os.ReadDir(dir)
		g.Expect(readErr).ToNot(HaveOccurred())
		g.Expect(entries).To(HaveLen(1))
		g.Expect(entries[0].Name()).To(Equal(snaputil.KubeProxyDisabledLockFile))
	})
}
