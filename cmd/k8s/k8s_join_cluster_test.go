package k8s_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func setupFakeSnapBinaries(t *testing.T) string {
	snapDir := t.TempDir()
	binDir := filepath.Join(snapDir, "bin")
	err := os.MkdirAll(binDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	script := []byte("#!/bin/sh\nexit 0\n")
	for _, bin := range []string{"kube-apiserver", "kube-controller-manager", "kube-scheduler", "kube-proxy", "kubelet"} {
		p := filepath.Join(binDir, bin)
		if err := os.WriteFile(p, script, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SNAP", snapDir)
	return snapDir
}

func TestK8sJoinClusterCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("join-cluster", "some-token")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("MissingTokenArg", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("join-cluster")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("accepts 1 arg(s), received 0"))
	})

	t.Run("InvalidAddress", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("join-cluster", "some-token", "--address=127.0.0.1:999999")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to parse the address"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("join-cluster", "some-token", "--address=127.0.0.1:6400")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("cannot query status")

		code, _ := cli.execute("join-cluster", "some-token", "--address=127.0.0.1:6400")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeAlreadyPartOfCluster", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = true

		code, _ := cli.execute("join-cluster", "some-token", "--address=127.0.0.1:6400")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is already part of a cluster"))
	})

	t.Run("MissingConfigFile", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("join-cluster", "some-token", "--file="+filepath.Join(t.TempDir(), "missing.yaml"), "--address=127.0.0.1:6400")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to read join configuration"))
	})

	t.Run("InvalidContainerdBaseDir", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("join-cluster", "some-token", "--containerd-base-dir=relative/path", "--address=127.0.0.1:6400")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("invalid containerd-base-dir value"))
	})

	t.Run("TimeoutWarning", func(t *testing.T) {
		g := NewWithT(t)
		setupFakeSnapBinaries(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false
		containerdDir := filepath.Join(t.TempDir(), "containerd")

		code, err := cli.execute("join-cluster", "some-token", "--timeout=1s", "--containerd-base-dir="+containerdDir, "--address=127.0.0.1:6400")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Timeout 1s is less than minimum of 3s"))
	})

	t.Run("JoinClusterError", func(t *testing.T) {
		g := NewWithT(t)
		setupFakeSnapBinaries(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false
		cli.MockClient.JoinClusterErr = errors.New("join RPC failed")
		containerdDir := filepath.Join(t.TempDir(), "containerd")

		code, _ := cli.execute("join-cluster", "some-token", "--containerd-base-dir="+containerdDir, "--address=127.0.0.1:6400")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to join the cluster using the provided token"))
	})

	t.Run("JoinClusterSuccess", func(t *testing.T) {
		setupFakeSnapBinaries(t)
		containerdDir := filepath.Join(t.TempDir(), "containerd")

		for _, format := range []string{"plain", "json", "yaml"} {
			g := NewWithT(t)
			cli := newTestCLI()
			cli.MockClient.NodeStatusInitialized = false

			code, err := cli.execute("join-cluster", "valid-join-token", "--name=new-node", "--containerd-base-dir="+containerdDir, "--address=127.0.0.1:6400", "--output-format="+format)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(code).To(Equal(0))
			g.Expect(cli.Stdout.String()).To(ContainSubstring("new-node"))
			g.Expect(cli.MockClient.JoinClusterCalledWith.Name).To(Equal("new-node"))
			g.Expect(cli.MockClient.JoinClusterCalledWith.Token).To(Equal("valid-join-token"))
			g.Expect(cli.MockClient.JoinClusterCalledWith.Address).To(Equal("127.0.0.1:6400"))
		}
	})
}
