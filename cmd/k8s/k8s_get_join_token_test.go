package k8s_test

import (
	"errors"
	"testing"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sGetJoinTokenCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("get-join-token", "control-plane-node")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("ControlPlaneMissingNodeName", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("get-join-token")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("A node name is required for control-plane nodes"))
	})

	t.Run("ControlPlaneSuccess", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetJoinTokenResponse = apiv2.GetJoinTokenResponse{EncodedToken: "cp-token-xyz"}

		code, err := cli.execute("get-join-token", "node-cp-1")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("cp-token-xyz"))
		g.Expect(cli.MockClient.GetJoinTokenCalledWith).To(Equal(apiv2.GetJoinTokenRequest{
			Name:   "node-cp-1",
			Worker: false,
			TTL:    24 * time.Hour,
		}))
	})

	t.Run("WorkerWithoutNodeName", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetJoinTokenResponse = apiv2.GetJoinTokenResponse{EncodedToken: "worker-token-123"}

		code, err := cli.execute("get-join-token", "--worker")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("worker-token-123"))
		g.Expect(cli.MockClient.GetJoinTokenCalledWith).To(Equal(apiv2.GetJoinTokenRequest{
			Name:   "",
			Worker: true,
			TTL:    24 * time.Hour,
		}))
	})

	t.Run("WorkerWithNodeNameAndTTL", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetJoinTokenResponse = apiv2.GetJoinTokenResponse{EncodedToken: "worker-token-456"}

		code, err := cli.execute("get-join-token", "--worker", "worker-node-1", "--expires-in=1h")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("worker-token-456"))
		g.Expect(cli.MockClient.GetJoinTokenCalledWith).To(Equal(apiv2.GetJoinTokenRequest{
			Name:   "worker-node-1",
			Worker: true,
			TTL:    1 * time.Hour,
		}))
	})

	t.Run("TimeoutWarning", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetJoinTokenResponse = apiv2.GetJoinTokenResponse{EncodedToken: "token"}

		code, err := cli.execute("get-join-token", "node-1", "--timeout=1s")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Timeout 1s is less than minimum of 3s"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("socket unreachable")

		code, _ := cli.execute("get-join-token", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("cannot read node status")

		code, _ := cli.execute("get-join-token", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("get-join-token", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("GetJoinTokenError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.GetJoinTokenErr = errors.New("token creation failed")

		code, _ := cli.execute("get-join-token", "node-1")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring(`Could not generate a join token for "node-1"`))
	})

	t.Run("TooManyArgs", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("get-join-token", "node-1", "node-2")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("accepts at most 1 arg"))
	})
}
