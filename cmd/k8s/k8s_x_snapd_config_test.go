package k8s_test

import (
	"errors"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sXSnapdConfigCmd(t *testing.T) {
	t.Run("Disable_Success", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, err := cli.execute("x-snapd-config", "disable")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.MockSnap.SnapctlSetCalledWith).NotTo(BeEmpty())
	})

	t.Run("Disable_Error", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockSnap.SnapctlSetErr = errors.New("snapctl set error")

		code, _ := cli.execute("x-snapd-config", "disable")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Error: failed to disable snapd configuration: failed to snapctl set"))
	})

	t.Run("Reconcile_OrbNone", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockSnap.Mock.SnapctlGet = map[string][]byte{
			"-d meta": []byte(`{"meta": {"orb": "none"}}`),
		}

		code, err := cli.execute("x-snapd-config", "reconcile")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Warning: meta.orb is none, skipping reconcile actions"))
	})

	t.Run("Reconcile_InvalidMeta", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockSnap.Mock.SnapctlGet = map[string][]byte{
			"-d meta": []byte(`{"meta": {"orb": "invalid-mode"}}`),
		}

		code, _ := cli.execute("x-snapd-config", "reconcile")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Error: failed to parse meta configuration"))
	})

	t.Run("Reconcile_ClientCreationError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockSnap.Mock.SnapctlGet = map[string][]byte{
			"-d meta": []byte(`{"meta": {"orb": "k8sd"}}`),
		}
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("x-snapd-config", "reconcile")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Error: failed to create k8sd client"))
	})

	t.Run("Reconcile_ControlPlaneSnapd", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockSnap.Mock.SnapctlGet = map[string][]byte{
			"-d meta": []byte(`{"meta": {"orb": "snapd"}}`),
			"-d dns network local-storage load-balancer ingress gateway": []byte(`{"dns": {"enabled": true}}`),
		}
		cli.MockClient.NodeStatusInitialized = true
		cli.MockClient.NodeStatusResponse = apiv2.NodeStatusResponse{
			NodeStatus: apiv2.NodeStatus{
				ClusterRole: apiv2.ClusterRoleControlPlane,
			},
		}

		code, err := cli.execute("x-snapd-config", "reconcile")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.MockClient.SetClusterConfigCalledWith.Config.DNS.Enabled).NotTo(BeNil())
		g.Expect(*cli.MockClient.SetClusterConfigCalledWith.Config.DNS.Enabled).To(BeTrue())
		g.Expect(cli.MockSnap.SnapctlSetCalledWith).NotTo(BeEmpty())
	})
}
