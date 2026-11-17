package k8s_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sXCAPICmd(t *testing.T) {
	t.Run("SetAuthToken_MissingArg", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("x-capi", "set-auth-token")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("accepts 1 arg(s), received 0"))
	})

	t.Run("SetAuthToken_EmptyToken", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("x-capi", "set-auth-token", "")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The token must be provided"))
	})

	t.Run("SetAuthToken_ClientError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("client creation failed")

		code, _ := cli.execute("x-capi", "set-auth-token", "tok-123")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("SetAuthToken_NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("node status error")

		code, _ := cli.execute("x-capi", "set-auth-token", "tok-123")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("SetAuthToken_NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("x-capi", "set-auth-token", "tok-123")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("SetAuthToken_APIError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.SetClusterAPIAuthTokenErr = errors.New("capi token save error")

		code, _ := cli.execute("x-capi", "set-auth-token", "tok-123")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to set the CAPI auth token"))
	})

	t.Run("SetAuthToken_Success", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, err := cli.execute("x-capi", "set-auth-token", "my-secret-capi-token")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.MockClient.SetClusterAPIAuthTokenCalledWith).To(Equal(apiv2.ClusterAPISetAuthTokenRequest{
			Token: "my-secret-capi-token",
		}))
	})

	t.Run("SetNodeToken_MissingArg", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("x-capi", "set-node-token")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("accepts 1 arg(s), received 0"))
	})

	t.Run("SetNodeToken_EmptyToken", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("x-capi", "set-node-token", "")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The token must be provided"))
	})

	t.Run("SetNodeToken_WriteError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockSnap.Mock.NodeTokenFile = filepath.Join(t.TempDir(), "missing-dir", "file.token")

		code, _ := cli.execute("x-capi", "set-node-token", "tok-node")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to write the node token to file"))
	})

	t.Run("SetNodeToken_Success", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		tokenPath := filepath.Join(t.TempDir(), "node-token")
		cli.MockSnap.Mock.NodeTokenFile = tokenPath

		code, err := cli.execute("x-capi", "set-node-token", "my-node-token-value")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))

		content, err := os.ReadFile(tokenPath)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(content)).To(Equal("my-node-token-value"))
	})
}
