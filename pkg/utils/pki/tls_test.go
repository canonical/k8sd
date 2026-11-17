package pkiutil_test

import (
	"crypto/x509/pkix"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkiutil "github.com/canonical/k8sd/pkg/utils/pki"
	. "github.com/onsi/gomega"
)

func TestLoadCACertPool(t *testing.T) {
	now := time.Now()
	caCertPEM, _, err := pkiutil.GenerateSelfSignedCA(pkix.Name{CommonName: "pool-ca"}, now.Add(-time.Hour), now.Add(24*time.Hour), 2048)
	g := NewWithT(t)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("ValidCAFile", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		caPath := filepath.Join(dir, "ca.crt")
		err := os.WriteFile(caPath, []byte(caCertPEM), 0o600)
		g.Expect(err).To(Not(HaveOccurred()))

		pool, err := pkiutil.LoadCACertPool(caPath)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(pool).ToNot(BeNil())
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		g := NewWithT(t)
		pool, err := pkiutil.LoadCACertPool("/non/existent/path/ca.crt")
		g.Expect(err).To(HaveOccurred())
		g.Expect(pool).To(BeNil())
	})

	t.Run("InvalidPEMContent", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		badPath := filepath.Join(dir, "bad-ca.crt")
		err := os.WriteFile(badPath, []byte("plain text not cert"), 0o600)
		g.Expect(err).To(Not(HaveOccurred()))

		pool, err := pkiutil.LoadCACertPool(badPath)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to append CA certificate"))
		g.Expect(pool).To(BeNil())
	})
}

func TestLoadTLSConfigFromPath(t *testing.T) {
	now := time.Now()
	caCertPEM, caKeyPEM, err := pkiutil.GenerateSelfSignedCA(pkix.Name{CommonName: "tls-ca"}, now.Add(-time.Hour), now.Add(24*time.Hour), 2048)
	g := NewWithT(t)
	g.Expect(err).To(Not(HaveOccurred()))

	caCert, caKey, err := pkiutil.LoadCertificate(caCertPEM, caKeyPEM)
	g.Expect(err).To(Not(HaveOccurred()))

	clientTmpl, err := pkiutil.GenerateCertificate(
		pkix.Name{CommonName: "client"},
		now.Add(-time.Hour),
		now.Add(12*time.Hour),
		false,
		[]string{"client.local"},
		nil,
	)
	g.Expect(err).To(Not(HaveOccurred()))

	clientCertPEM, clientKeyPEM, err := pkiutil.SignCertificate(clientTmpl, 2048, caCert, nil, caKey)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		certPath := filepath.Join(dir, "client.crt")
		keyPath := filepath.Join(dir, "client.key")
		caPath := filepath.Join(dir, "ca.crt")

		g.Expect(os.WriteFile(certPath, []byte(clientCertPEM), 0o600)).To(Succeed())
		g.Expect(os.WriteFile(keyPath, []byte(clientKeyPEM), 0o600)).To(Succeed())
		g.Expect(os.WriteFile(caPath, []byte(caCertPEM), 0o600)).To(Succeed())

		cfg, err := pkiutil.LoadTLSConfigFromPath(certPath, keyPath, caPath)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cfg).ToNot(BeNil())
		g.Expect(cfg.Certificates).To(HaveLen(1))
		g.Expect(cfg.RootCAs).ToNot(BeNil())
	})

	t.Run("MissingKeyPairFiles", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		caPath := filepath.Join(dir, "ca.crt")
		g.Expect(os.WriteFile(caPath, []byte(caCertPEM), 0o600)).To(Succeed())

		cfg, err := pkiutil.LoadTLSConfigFromPath("/missing/cert.crt", "/missing/key.key", caPath)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to load client certificate or key"))
		g.Expect(cfg).To(BeNil())
	})

	t.Run("MissingCAFile", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		certPath := filepath.Join(dir, "client.crt")
		keyPath := filepath.Join(dir, "client.key")
		g.Expect(os.WriteFile(certPath, []byte(clientCertPEM), 0o600)).To(Succeed())
		g.Expect(os.WriteFile(keyPath, []byte(clientKeyPEM), 0o600)).To(Succeed())

		cfg, err := pkiutil.LoadTLSConfigFromPath(certPath, keyPath, "/missing/ca.crt")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to load CA certificate"))
		g.Expect(cfg).To(BeNil())
	})

	t.Run("CorruptedCAFile", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		certPath := filepath.Join(dir, "client.crt")
		keyPath := filepath.Join(dir, "client.key")
		caPath := filepath.Join(dir, "ca.crt")

		g.Expect(os.WriteFile(certPath, []byte(clientCertPEM), 0o600)).To(Succeed())
		g.Expect(os.WriteFile(keyPath, []byte(clientKeyPEM), 0o600)).To(Succeed())
		g.Expect(os.WriteFile(caPath, []byte("bad ca data"), 0o600)).To(Succeed())

		cfg, err := pkiutil.LoadTLSConfigFromPath(certPath, keyPath, caPath)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to load CA certificate"))
		g.Expect(cfg).To(BeNil())
	})
}
