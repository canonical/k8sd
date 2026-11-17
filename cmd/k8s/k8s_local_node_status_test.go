package k8s_test

import (
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sLocalNodeStatusCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("local-node-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("local-node-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("status query error")

		code, _ := cli.execute("local-node-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("local-node-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("FormatsSuccess", func(t *testing.T) {
		nodeStatus := apiv2.NodeStatus{
			Name:          "test-node",
			Address:       "192.168.1.100:1234",
			ClusterRole:   apiv2.ClusterRoleControlPlane,
			DatastoreRole: apiv2.DatastoreRoleVoter,
		}

		for _, format := range []string{"plain", "json", "yaml"} {
			g := NewWithT(t)
			cli := newTestCLI()
			cli.MockClient.NodeStatusResponse.NodeStatus = nodeStatus

			code, err := cli.execute("local-node-status", "--output-format="+format)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(code).To(Equal(0))
			g.Expect(cli.Stdout.String()).To(ContainSubstring("test-node"))
		}
	})
}
