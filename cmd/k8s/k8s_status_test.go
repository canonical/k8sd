package k8s_test

import (
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"
)

func sampleClusterStatus() apiv2.ClusterStatus {
	return apiv2.ClusterStatus{
		Ready: true,
		Members: []apiv2.NodeStatus{
			{
				Name:          "node-1",
				Address:       "10.0.0.1:1234",
				ClusterRole:   apiv2.ClusterRoleControlPlane,
				DatastoreRole: apiv2.DatastoreRoleVoter,
			},
		},
		Datastore: apiv2.Datastore{
			Type: "etcd",
		},
		Network: apiv2.FeatureStatus{
			Message: "network enabled",
		},
		DNS: apiv2.FeatureStatus{
			Message: "dns enabled",
		},
		Config: apiv2.UserFacingClusterConfig{
			Network: apiv2.NetworkConfig{
				Enabled: ptr.To(true),
			},
			CloudProvider: ptr.To("external"),
		},
	}
}

func TestK8sStatusCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("connection refused")

		code, _ := cli.execute("status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("node status error")

		code, _ := cli.execute("status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("ClusterStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.ClusterStatusErr = errors.New("cluster status error")

		code, _ := cli.execute("status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to retrieve the cluster status"))
	})

	t.Run("TimeoutWarning", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.ClusterStatusResponse.ClusterStatus = sampleClusterStatus()

		code, _ := cli.execute("status", "--timeout=1s")
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Timeout 1s is less than minimum of 3s"))
	})

	t.Run("WaitReadyFlag", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.ClusterStatusResponse.ClusterStatus = sampleClusterStatus()

		code, err := cli.execute("status", "--wait-ready")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
	})
	t.Run("FormatsAndConfigSilenced", func(t *testing.T) {
		for _, format := range []string{"plain", "json", "yaml"} {
			g := NewWithT(t)
			cli := newTestCLI()
			cli.MockClient.ClusterStatusResponse.ClusterStatus = sampleClusterStatus()

			code, err := cli.execute("status", "--output-format="+format)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(code).To(Equal(0))
			out := cli.Stdout.String()
			g.Expect(out).To(ContainSubstring("ready"))
			g.Expect(out).NotTo(ContainSubstring("external"))
		}
	})

	t.Run("ExtraArgs", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("status", "extra-arg")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring(`unknown command "extra-arg" for "k8s status"`))
	})
}
