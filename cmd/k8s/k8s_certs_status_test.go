package k8s_test

import (
	"errors"
	"testing"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sCertsStatusCmd(t *testing.T) {
	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("certs-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("cannot get node status")

		code, _ := cli.execute("certs-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("certs-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("CertificatesStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.CertificatesStatusErr = errors.New("failed to query status")

		code, _ := cli.execute("certs-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to retrieve certificate status"))
	})

	t.Run("InvalidCertificateExpirationDate", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.CertificatesStatusResponse = apiv2.CertificatesStatusResponse{
			Certificates: []apiv2.CertificateStatus{
				{
					Name:                 "apiserver",
					Expires:              "invalid-date",
					CertificateAuthority: "kubernetes-ca",
					ExternallyManaged:    false,
				},
			},
		}

		code, _ := cli.execute("certs-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("failed to parse expiration date for certificate apiserver"))
	})

	t.Run("InvalidCAExpirationDate", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		future := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
		cli.MockClient.CertificatesStatusResponse = apiv2.CertificatesStatusResponse{
			Certificates: []apiv2.CertificateStatus{
				{
					Name:                 "apiserver",
					Expires:              future,
					CertificateAuthority: "kubernetes-ca",
					ExternallyManaged:    false,
				},
			},
			CertificateAuthorities: []apiv2.CertificateAuthorityStatus{
				{
					Name:              "kubernetes-ca",
					Expires:           "not-rfc3339",
					ExternallyManaged: true,
				},
			},
		}

		code, _ := cli.execute("certs-status")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("failed to parse expiration date for certificate authority kubernetes-ca"))
	})

	t.Run("SuccessOutput", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		future := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
		cli.MockClient.CertificatesStatusResponse = apiv2.CertificatesStatusResponse{
			Certificates: []apiv2.CertificateStatus{
				{
					Name:                 "apiserver",
					Expires:              future,
					CertificateAuthority: "kubernetes-ca",
					ExternallyManaged:    false,
				},
				{
					Name:                 "front-proxy-client",
					Expires:              future,
					CertificateAuthority: "front-proxy-ca",
					ExternallyManaged:    true,
				},
			},
			CertificateAuthorities: []apiv2.CertificateAuthorityStatus{
				{
					Name:              "kubernetes-ca",
					Expires:           future,
					ExternallyManaged: false,
				},
			},
		}

		code, err := cli.execute("certs-status")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))

		stdout := cli.Stdout.String()
		g.Expect(stdout).To(ContainSubstring("CERTIFICATE"))
		g.Expect(stdout).To(ContainSubstring("apiserver"))
		g.Expect(stdout).To(ContainSubstring("front-proxy-client"))
		g.Expect(stdout).To(ContainSubstring("CERTIFICATE AUTHORITY"))
		g.Expect(stdout).To(ContainSubstring("kubernetes-ca"))
		g.Expect(stdout).To(ContainSubstring("yes"))
		g.Expect(stdout).To(ContainSubstring("no"))
	})
}
