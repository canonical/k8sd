package k8s_test

import (
	"errors"
	"testing"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sRemoveNodeCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("remove-node", "node-to-remove")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("MissingArg", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("remove-node")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("accepts 1 arg(s), received 0"))
	})

	t.Run("TooManyArgs", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("remove-node", "node-1", "node-2")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("accepts 1 arg(s), received 2"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("remove-node", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("node status error")

		code, _ := cli.execute("remove-node", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("remove-node", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("TimeoutWarning", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, err := cli.execute("remove-node", "node-1", "--timeout=1s")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Timeout 1s is less than minimum of 3s"))
	})

	t.Run("RemoveNodeError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.RemoveNodeErr = errors.New("cannot remove member")

		code, _ := cli.execute("remove-node", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring(`Failed to remove node "node-1" from the cluster`))
	})

	t.Run("SuccessDefaultAndForce", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, err := cli.execute("remove-node", "worker-1")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.MockClient.RemoveNodeCalledWith).To(Equal(apiv2.RemoveNodeRequest{
			Name:    "worker-1",
			Force:   false,
			Timeout: 90 * time.Second,
		}))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("Removed worker-1 from cluster."))

		// Test with --force and json format
		cli = newTestCLI()
		code, err = cli.execute("remove-node", "control-plane-2", "--force", "--output-format=json")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.MockClient.RemoveNodeCalledWith.Force).To(BeTrue())
		g.Expect(cli.Stdout.String()).To(ContainSubstring(`"name": "control-plane-2"`))
	})
}
