package k8s_test

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"
)

func TestK8sSetCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("set", "dns.cluster-domain=cluster.local")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("MissingArgs", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("set")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("requires at least 1 arg(s)"))
	})

	t.Run("InvalidOption", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("set", "unknown.field=true")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring(`Invalid option "unknown.field=true"`))
	})

	t.Run("TimeoutWarning", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, err := cli.execute("set", "dns.enabled=true", "--timeout=1s")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Timeout 1s is less than minimum of 3s"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("set", "dns.enabled=true")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("SetClusterConfigError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.SetClusterConfigErr = errors.New("failed to apply changes")

		code, _ := cli.execute("set", "dns.enabled=true")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to apply requested cluster configuration changes"))
	})

	t.Run("SuccessFormats", func(t *testing.T) {
		for _, format := range []string{"plain", "json", "yaml"} {
			g := NewWithT(t)
			cli := newTestCLI()

			code, err := cli.execute("set", "dns.cluster-domain=my.domain", "--output-format="+format)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(code).To(Equal(0))
			g.Expect(cli.MockClient.SetClusterConfigCalledWith.Config.DNS.ClusterDomain).NotTo(BeNil())
			g.Expect(*cli.MockClient.SetClusterConfigCalledWith.Config.DNS.ClusterDomain).To(Equal("my.domain"))
			if format == "plain" {
				g.Expect(cli.Stdout.String()).To(Equal("Configuration updated.\n"))
			} else {
				g.Expect(cli.Stdout.String()).To(ContainSubstring("my.domain"))
			}
		}
	})
}
