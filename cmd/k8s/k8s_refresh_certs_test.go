package k8s_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestK8sRefreshCertsCmd(t *testing.T) {
	t.Run("NonRoot", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Env.Getuid = func() int { return 1000 }

		code, _ := cli.execute("refresh-certs", "--expires-in=1h")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("You do not have enough permissions"))
	})

	t.Run("PreRunMissingExpiresIn", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("refresh-certs")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("the --expires-in flag is required when not using --external-certificates"))
	})

	t.Run("PreRunExternalWithExpiresIn", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("refresh-certs", "--external-certificates=certs.yaml", "--expires-in=1h")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("--external-certificates cannot be used together with --expires-in, --extra-sans, or --certificates"))
	})

	t.Run("PreRunExternalWithExtraSANs", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("refresh-certs", "--external-certificates=certs.yaml", "--extra-sans=example.com")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("--external-certificates cannot be used together with"))
	})

	t.Run("PreRunExternalWithCertificates", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("refresh-certs", "--external-certificates=certs.yaml", "--certificates=apiserver")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("--external-certificates cannot be used together with"))
	})

	t.Run("ClientCreationFailure", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.K8sdClientErr = errors.New("cannot create client")

		code, _ := cli.execute("refresh-certs", "--expires-in=1h")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to create a k8sd client"))
	})

	t.Run("NodeStatusError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusErr = errors.New("node status check failed")

		code, _ := cli.execute("refresh-certs", "--expires-in=1h")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to check the current node status"))
	})

	t.Run("NodeNotInitialized", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.NodeStatusInitialized = false

		code, _ := cli.execute("refresh-certs", "--expires-in=1h")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("The node is not part of a Kubernetes cluster"))
	})

	t.Run("ExternalCertsFromStdinInvalidYAML", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Stdin.WriteString("invalid: yaml: [")

		code, _ := cli.execute("refresh-certs", "--external-certificates=-")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to parse certificates file"))
	})

	t.Run("ExternalCertsFromFileNotFound", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("refresh-certs", "--external-certificates="+filepath.Join(t.TempDir(), "missing.yaml"))
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("failed to read file"))
	})

	t.Run("ExternalCertsFromFileSuccess", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		tmpFile := filepath.Join(t.TempDir(), "certs.yaml")
		yamlContent := "apiserver-crt: my-cert-pem\napiserver-key: my-key-pem\n"
		err := os.WriteFile(tmpFile, []byte(yamlContent), 0o600)
		g.Expect(err).NotTo(HaveOccurred())

		code, err := cli.execute("refresh-certs", "--external-certificates="+tmpFile)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("External certificates have been successfully updated."))
		g.Expect(cli.MockClient.RefreshCertificatesUpdateCalledWith.GetAPIServerCert()).To(Equal("my-cert-pem"))
		g.Expect(cli.MockClient.RefreshCertificatesUpdateCalledWith.GetAPIServerKey()).To(Equal("my-key-pem"))
	})

	t.Run("ExternalCertsUpdateError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.Stdin.WriteString("{}\n")
		cli.MockClient.RefreshCertificatesUpdateErr = errors.New("update rejected")

		code, _ := cli.execute("refresh-certs", "--external-certificates=-")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to refresh external certificates: update rejected"))
	})

	t.Run("InternalCertificatesInvalidTTL", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, _ := cli.execute("refresh-certs", "--expires-in=invalid-ttl")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to parse TTL"))
	})

	t.Run("InternalCertificatesPlanError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.RefreshCertificatesPlanErr = errors.New("plan error")

		code, _ := cli.execute("refresh-certs", "--expires-in=1h")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to get the certificates refresh plan"))
	})

	t.Run("InternalCertificatesPlanWithCSRsAndRunSuccess", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.RefreshCertificatesPlanResponse = apiv2.RefreshCertificatesPlanResponse{
			Seed: 123,
			CertificateSigningRequests: []string{
				"csr-worker-1",
				"csr-worker-2",
			},
		}
		cli.MockClient.RefreshCertificatesRunResponse = apiv2.RefreshCertificatesRunResponse{
			ExpirationSeconds: 1700000000,
		}

		code, err := cli.execute("refresh-certs", "--expires-in=24h", "--certificates=kubelet,apiserver", "--extra-sans=k8s.example.com")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))

		stdout := cli.Stdout.String()
		g.Expect(stdout).To(ContainSubstring("k8s kubectl certificate approve csr-worker-1"))
		g.Expect(stdout).To(ContainSubstring("k8s kubectl certificate approve csr-worker-2"))
		g.Expect(stdout).To(ContainSubstring("Waiting for certificates to be created..."))
		g.Expect(stdout).To(ContainSubstring("Certificates have been successfully refreshed"))
	})

	t.Run("InternalCertificatesRunError", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()
		cli.MockClient.RefreshCertificatesPlanResponse = apiv2.RefreshCertificatesPlanResponse{Seed: 1}
		cli.MockClient.RefreshCertificatesRunErr = errors.New("failed to issue certs")

		code, _ := cli.execute("refresh-certs", "--expires-in=1h")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Failed to refresh the certificates"))
	})
}
