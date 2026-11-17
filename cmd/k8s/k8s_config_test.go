package k8s_test

import (
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sConfigCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("config")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("config")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("node status error")

		code, _ := cli.execute("config")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("config")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("TimeoutWarning", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.KubeConfigResponse = apiv2.KubeConfigResponse{KubeConfig: "apiVersion: v1"}

		code, err := cli.execute("config", "--timeout=1s")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Timeout 1s is less than minimum of 3s"))
	})

	t.Run("KubeConfigError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.KubeConfigErr = errors.New("failed to generate kubeconfig")

		code, _ := cli.execute("config", "--server=https://my-server:6443")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring(`Failed to generate an admin kubeconfig for "https://my-server:6443"`))
	})

	t.Run("SuccessWithCustomServer", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.KubeConfigResponse = apiv2.KubeConfigResponse{
			KubeConfig: "apiVersion: v1\nkind: Config\nclusters: []",
		}

		code, err := cli.execute("config", "--server=https://k8s.example.com:6443")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("apiVersion: v1"))
		g.Expect(cli.MockClient.KubeConfigCalledWith).To(Equal(apiv2.KubeConfigRequest{
			Server: "https://k8s.example.com:6443",
		}))
	})
}
