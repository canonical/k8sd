package k8s_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/snap"
	. "github.com/onsi/gomega"
)

type mockCleanup struct {
	cleanupNetworkErr error
}

func (m *mockCleanup) CleanupNetwork(_ context.Context, _ snap.Snap) error {
	return m.cleanupNetworkErr
}

func TestK8sXCleanupCmd(t *testing.T) {
	origCleanup := features.Cleanup
	defer func() {
		features.Cleanup = origCleanup
	}()

	t.Run("NetworkSuccess", func(t *testing.T) {
		g := NewWithT(t)
		mock := &mockCleanup{cleanupNetworkErr: nil}
		features.Cleanup = mock

		cli := newTestCLI()
		cli.MockSnap.Mock.K8sBinDir = t.TempDir()

		code, err := cli.execute("x-cleanup", "network")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
	})

	t.Run("NetworkError", func(t *testing.T) {
		g := NewWithT(t)
		mock := &mockCleanup{cleanupNetworkErr: errors.New("cleanup failed")}
		features.Cleanup = mock

		cli := newTestCLI()
		cli.MockSnap.Mock.K8sBinDir = t.TempDir()

		code, _ := cli.execute("x-cleanup", "network")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Error: failed to cleanup network: cleanup failed"))
	})

	t.Run("ContainerdSuccess", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		lockDir := t.TempDir()
		rootDir := filepath.Join(t.TempDir(), "containerd-root")
		err := os.MkdirAll(rootDir, 0o755)
		g.Expect(err).NotTo(HaveOccurred())

		cli.MockSnap.Mock.LockFilesDir = lockDir
		cli.MockSnap.Mock.ContainerdRootDir = rootDir
		cli.MockSnap.Mock.ContainerdSocketDir = filepath.Join(t.TempDir(), "socket")
		cli.MockSnap.Mock.ContainerdConfigDir = filepath.Join(t.TempDir(), "config")
		cli.MockSnap.Mock.CNIBinDir = filepath.Join(t.TempDir(), "cni")

		lockFile := filepath.Join(lockDir, "containerd-root-dir")
		err = os.WriteFile(lockFile, []byte(rootDir), 0o600)
		g.Expect(err).NotTo(HaveOccurred())

		code, err := cli.execute("x-cleanup", "containerd")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))

		_, err = os.Stat(rootDir)
		g.Expect(os.IsNotExist(err)).To(BeTrue())
		_, err = os.Stat(lockFile)
		g.Expect(os.IsNotExist(err)).To(BeTrue())
	})
}
