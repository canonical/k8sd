package k8s_test

import (
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sKubectlCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("kubectl", "get", "pods")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("MissingKubeconfig", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockSnap.Mock.KubernetesConfigDir = t.TempDir() // empty dir, no admin.conf or kubelet.conf
		cli.Env.Environ = []string{}

		code, _ := cli.execute("kubectl", "get", "pods")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("do not exist; please set KUBECONFIG"))
	})
}

func TestK8sHelmCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("helm", "list")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("helm", "list")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("node status error")

		code, _ := cli.execute("helm", "list")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("helm", "list")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("WorkerNodeNotAllowed", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusResponse = apiv2.NodeStatusResponse{
			NodeStatus: apiv2.NodeStatus{
				ClusterRole: apiv2.ClusterRoleWorker,
			},
		}

		code, _ := cli.execute("helm", "list")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("k8s helm commands are not allowed on worker nodes"))
	})
}

func TestK8sInspectCmd(t *testing.T) {
	t.Run("HelpFlag", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, err := cli.execute("inspect", "--help")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("inspection report"))
	})

	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("inspect", "/tmp/report.tar.gz")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})
}
