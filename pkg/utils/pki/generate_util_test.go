package pkiutil_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"
	"time"

	pkiutil "github.com/canonical/k8sd/pkg/utils/pki"
	. "github.com/onsi/gomega"
)

func TestGenerateSerialNumber(t *testing.T) {
	g := NewWithT(t)

	sn1, err := pkiutil.GenerateSerialNumber()
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(sn1).ToNot(BeNil())
	g.Expect(sn1.Sign()).To(Equal(1)) // positive

	sn2, err := pkiutil.GenerateSerialNumber()
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(sn2).ToNot(BeNil())
	g.Expect(sn1).ToNot(Equal(sn2))
}

func TestGenerateCertificate(t *testing.T) {
	notBefore := time.Now()
	notAfter := notBefore.Add(24 * time.Hour)
	subject := pkix.Name{
		CommonName:   "test-cert",
		Organization: []string{"Canonical"},
	}
	dnsSANs := []string{"k8s.local", "api.k8s.local"}
	ipSANs := []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("192.168.1.1")}

	t.Run("CA=true", func(t *testing.T) {
		g := NewWithT(t)
		cert, err := pkiutil.GenerateCertificate(subject, notBefore, notAfter, true, dnsSANs, ipSANs)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cert).ToNot(BeNil())
		g.Expect(cert.IsCA).To(BeTrue())
		g.Expect(cert.BasicConstraintsValid).To(BeTrue())
		g.Expect(cert.KeyUsage).To(Equal(x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign))
		g.Expect(cert.ExtKeyUsage).To(ConsistOf(x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth))
		g.Expect(cert.Subject.CommonName).To(Equal("test-cert"))
		g.Expect(cert.DNSNames).To(Equal(dnsSANs))
		g.Expect(cert.IPAddresses).To(Equal(ipSANs))
		g.Expect(cert.NotBefore).To(Equal(notBefore))
		g.Expect(cert.NotAfter).To(Equal(notAfter))
	})

	t.Run("CA=false", func(t *testing.T) {
		g := NewWithT(t)
		cert, err := pkiutil.GenerateCertificate(subject, notBefore, notAfter, false, dnsSANs, ipSANs)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cert).ToNot(BeNil())
		g.Expect(cert.IsCA).To(BeFalse())
		g.Expect(cert.BasicConstraintsValid).To(BeTrue())
		expectedKU := x509.KeyUsageKeyEncipherment | x509.KeyUsageDataEncipherment | x509.KeyUsageDigitalSignature
		g.Expect(cert.KeyUsage).To(Equal(expectedKU))
		g.Expect(cert.ExtKeyUsage).To(ConsistOf(x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth))
	})
}

func TestSignCertificate(t *testing.T) {
	now := time.Now()

	t.Run("NilPubAndPrivGeneratesKey", func(t *testing.T) {
		g := NewWithT(t)

		caTmpl, err := pkiutil.GenerateCertificate(
			pkix.Name{CommonName: "self-signer-ca"},
			now.Add(-time.Hour),
			now.Add(24*time.Hour),
			true,
			nil,
			nil,
		)
		g.Expect(err).To(Not(HaveOccurred()))

		certPEM, keyPEM, err := pkiutil.SignCertificate(caTmpl, 2048, caTmpl, nil, nil)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(certPEM).ToNot(BeEmpty())
		g.Expect(keyPEM).ToNot(BeEmpty())

		cert, key, err := pkiutil.LoadCertificate(certPEM, keyPEM)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cert.Subject.CommonName).To(Equal("self-signer-ca"))
		g.Expect(cert.CheckSignatureFrom(cert)).To(Succeed())
		g.Expect(key).ToNot(BeNil())
	})

	t.Run("ExplicitPubAndPriv", func(t *testing.T) {
		g := NewWithT(t)

		caCertPEM, caKeyPEM, err := pkiutil.GenerateSelfSignedCA(pkix.Name{CommonName: "signer-ca"}, now.Add(-time.Hour), now.Add(24*time.Hour), 2048)
		g.Expect(err).To(Not(HaveOccurred()))

		caCert, caKey, err := pkiutil.LoadCertificate(caCertPEM, caKeyPEM)
		g.Expect(err).To(Not(HaveOccurred()))

		leafTmpl, err := pkiutil.GenerateCertificate(
			pkix.Name{CommonName: "leaf"},
			now.Add(-time.Hour),
			now.Add(12*time.Hour),
			false,
			[]string{"leaf.example.com"},
			nil,
		)
		g.Expect(err).To(Not(HaveOccurred()))

		privKey, err := rsa.GenerateKey(rand.Reader, 2048)
		g.Expect(err).To(Not(HaveOccurred()))

		certPEM, keyPEM, err := pkiutil.SignCertificate(leafTmpl, 2048, caCert, &privKey.PublicKey, caKey)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(certPEM).ToNot(BeEmpty())
		g.Expect(keyPEM).ToNot(BeEmpty())

		cert, _, err := pkiutil.LoadCertificate(certPEM, "")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cert.Subject.CommonName).To(Equal("leaf"))
		g.Expect(cert.Issuer.CommonName).To(Equal("signer-ca"))
		g.Expect(cert.CheckSignatureFrom(caCert)).To(Succeed())

		childKey, err := pkiutil.LoadRSAPrivateKey(keyPEM)
		g.Expect(err).To(Not(HaveOccurred()))
		rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
		g.Expect(ok).To(BeTrue())
		g.Expect(rsaPub.Equal(&childKey.PublicKey)).To(BeTrue())
	})
}

func TestGenerateRSAKey(t *testing.T) {
	g := NewWithT(t)

	privPEM, pubPEM, err := pkiutil.GenerateRSAKey(2048)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(privPEM).ToNot(BeEmpty())
	g.Expect(pubPEM).ToNot(BeEmpty())

	priv, err := pkiutil.LoadRSAPrivateKey(privPEM)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(priv).ToNot(BeNil())

	pub, err := pkiutil.LoadRSAPublicKey(pubPEM)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(pub).ToNot(BeNil())
	g.Expect(pub.N).To(Equal(priv.PublicKey.N))
}

func TestGenerateCSR(t *testing.T) {
	g := NewWithT(t)

	subject := pkix.Name{
		CommonName:   "csr-test",
		Organization: []string{"Canonical"},
	}
	dnsSANs := []string{"csr.example.com"}
	ipSANs := []net.IP{net.ParseIP("10.1.1.1")}

	csrPEM, keyPEM, err := pkiutil.GenerateCSR(subject, 2048, dnsSANs, ipSANs)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(csrPEM).ToNot(BeEmpty())
	g.Expect(keyPEM).ToNot(BeEmpty())

	csr, err := pkiutil.LoadCertificateRequest(csrPEM)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(csr.Subject.CommonName).To(Equal("csr-test"))
	g.Expect(csr.DNSNames).To(Equal(dnsSANs))
	g.Expect(csr.IPAddresses).To(HaveLen(1))
	g.Expect(csr.IPAddresses[0].Equal(ipSANs[0])).To(BeTrue())

	key, err := pkiutil.LoadRSAPrivateKey(keyPEM)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(key).ToNot(BeNil())
}
