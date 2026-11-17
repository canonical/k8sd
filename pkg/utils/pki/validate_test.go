package pkiutil_test

import (
	"crypto/x509/pkix"
	"net"
	"testing"
	"time"

	pkiutil "github.com/canonical/k8sd/pkg/utils/pki"
	. "github.com/onsi/gomega"
)

func TestCertCheck_ValidateCert(t *testing.T) {
	now := time.Now()

	// CA cert
	caCertPEM, caKeyPEM, err := pkiutil.GenerateSelfSignedCA(pkix.Name{
		CommonName:   "root-ca",
		Organization: []string{"Canonical", "Engineering"},
	}, now.Add(-time.Hour), now.Add(24*time.Hour), 2048)
	g := NewWithT(t)
	g.Expect(err).To(Not(HaveOccurred()))

	caCert, caKey, err := pkiutil.LoadCertificate(caCertPEM, caKeyPEM)
	g.Expect(err).To(Not(HaveOccurred()))

	// Leaf cert signed by CA
	leafCertTemplate, err := pkiutil.GenerateCertificate(
		pkix.Name{
			CommonName:   "server.local",
			Organization: []string{"Canonical"},
		},
		now.Add(-time.Hour),
		now.Add(12*time.Hour),
		false,
		[]string{"server.local", "*.canonical.com"},
		[]net.IP{net.ParseIP("127.0.0.1")},
	)
	g.Expect(err).To(Not(HaveOccurred()))

	leafCertPEM, leafKeyPEM, err := pkiutil.SignCertificate(leafCertTemplate, 2048, caCert, nil, caKey)
	g.Expect(err).To(Not(HaveOccurred()))

	leafCert, _, err := pkiutil.LoadCertificate(leafCertPEM, leafKeyPEM)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("NilCert", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{}
		err := check.ValidateCert(nil)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("no certificate specified"))
	})

	t.Run("ValidMatchingCN", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			CN:              "server.local",
			AllowSelfSigned: true,
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("MismatchedCN", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			CN:              "wrong.local",
			AllowSelfSigned: true,
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid certificate CN, expected: wrong.local, actual: server.local"))
	})

	t.Run("ValidMatchingOrganizations", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			O:               []string{"Canonical"},
			AllowSelfSigned: true,
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("MissingOrganization", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			O:               []string{"NonExistentOrg"},
			AllowSelfSigned: true,
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("missing cert organization: NonExistentOrg"))
	})

	t.Run("NotYetValid", func(t *testing.T) {
		g := NewWithT(t)
		futureCertPEM, _, err := pkiutil.GenerateSelfSignedCA(
			pkix.Name{CommonName: "future"},
			now.Add(2*time.Hour),
			now.Add(24*time.Hour),
			2048,
		)
		g.Expect(err).To(Not(HaveOccurred()))
		futureCert, _, err := pkiutil.LoadCertificate(futureCertPEM, "")
		g.Expect(err).To(Not(HaveOccurred()))

		check := pkiutil.CertCheck{AllowSelfSigned: true}
		err = check.ValidateCert(futureCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid certificate, not valid before"))
	})

	t.Run("ExpiredCert", func(t *testing.T) {
		g := NewWithT(t)
		expiredCertPEM, _, err := pkiutil.GenerateSelfSignedCA(
			pkix.Name{CommonName: "expired"},
			now.Add(-24*time.Hour),
			now.Add(-time.Hour),
			2048,
		)
		g.Expect(err).To(Not(HaveOccurred()))
		expiredCert, _, err := pkiutil.LoadCertificate(expiredCertPEM, "")
		g.Expect(err).To(Not(HaveOccurred()))

		check := pkiutil.CertCheck{AllowSelfSigned: true}
		err = check.ValidateCert(expiredCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("certificate expired since"))
	})

	t.Run("ValidateAgainstValidCAPEM", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			CaPEM:           caCertPEM,
			AllowSelfSigned: false,
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("ValidateAgainstInvalidCAPEM", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			CaPEM:           "not-a-valid-ca-pem",
			AllowSelfSigned: false,
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid CA certificate"))
	})

	t.Run("ValidateAgainstUntrustedCAPEM", func(t *testing.T) {
		g := NewWithT(t)
		otherCAPEM, _, err := pkiutil.GenerateSelfSignedCA(pkix.Name{CommonName: "other-ca"}, now.Add(-time.Hour), now.Add(24*time.Hour), 2048)
		g.Expect(err).To(Not(HaveOccurred()))

		check := pkiutil.CertCheck{
			CaPEM:           otherCAPEM,
			AllowSelfSigned: false,
		}
		err = check.ValidateCert(leafCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("certificate validation failure"))
	})

	t.Run("ValidateSelfSignedDisallowedWithoutCA", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			AllowSelfSigned: false,
		}
		err := check.ValidateCert(caCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("certificate validation failure"))
	})

	t.Run("MatchingDNSSANs", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			AllowSelfSigned: true,
			DNSSANs:         []string{"server.local", "test.canonical.com"},
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("MismatchedDNSSAN", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			AllowSelfSigned: true,
			DNSSANs:         []string{"unmatched.example.com"},
		}
		err := check.ValidateCert(leafCert)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("certificate dns name (unmatched.example.com) validation failure"))
	})
}

func TestCertCheck_ValidateKeypair(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM, err := pkiutil.GenerateSelfSignedCA(pkix.Name{
		CommonName: "keypair-test",
	}, now.Add(-time.Hour), now.Add(24*time.Hour), 2048)

	g := NewWithT(t)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			CN:              "keypair-test",
			AllowSelfSigned: true,
		}
		err := check.ValidateKeypair(certPEM, keyPEM)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("InvalidCertificatePEM", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			AllowSelfSigned: true,
		}
		err := check.ValidateKeypair("invalid", keyPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse certificate"))
	})

	t.Run("FailedValidation", func(t *testing.T) {
		g := NewWithT(t)
		check := pkiutil.CertCheck{
			CN:              "wrong-cn",
			AllowSelfSigned: true,
		}
		err := check.ValidateKeypair(certPEM, keyPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid certificate CN"))
	})
}
