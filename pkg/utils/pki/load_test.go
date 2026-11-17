package pkiutil_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkiutil "github.com/canonical/k8sd/pkg/utils/pki"
	. "github.com/onsi/gomega"
)

func TestLoadCertificate(t *testing.T) {
	g := NewWithT(t)

	notBefore := time.Now().Add(-time.Hour)
	notAfter := notBefore.Add(24 * time.Hour)
	validCertPEM, validKeyPEM, err := pkiutil.GenerateSelfSignedCA(pkix.Name{CommonName: "test-ca"}, notBefore, notAfter, 2048)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("ValidCertAndKey", func(t *testing.T) {
		g := NewWithT(t)
		cert, key, err := pkiutil.LoadCertificate(validCertPEM, validKeyPEM)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cert).ToNot(BeNil())
		g.Expect(cert.Subject.CommonName).To(Equal("test-ca"))
		g.Expect(key).ToNot(BeNil())
	})

	t.Run("ValidCertEmptyKey", func(t *testing.T) {
		g := NewWithT(t)
		cert, key, err := pkiutil.LoadCertificate(validCertPEM, "")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cert).ToNot(BeNil())
		g.Expect(key).To(BeNil())
	})

	t.Run("InvalidCertPEMBlock", func(t *testing.T) {
		g := NewWithT(t)
		cert, key, err := pkiutil.LoadCertificate("not-a-pem", validKeyPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse certificate PEM"))
		g.Expect(cert).To(BeNil())
		g.Expect(key).To(BeNil())
	})

	t.Run("InvalidCertDERBytes", func(t *testing.T) {
		g := NewWithT(t)
		corruptedCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")})
		cert, key, err := pkiutil.LoadCertificate(string(corruptedCertPEM), validKeyPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse certificate"))
		g.Expect(cert).To(BeNil())
		g.Expect(key).To(BeNil())
	})

	t.Run("ValidCertInvalidKey", func(t *testing.T) {
		g := NewWithT(t)
		cert, key, err := pkiutil.LoadCertificate(validCertPEM, "invalid-key")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to load RSA private key"))
		g.Expect(cert).To(BeNil())
		g.Expect(key).To(BeNil())
	})
}

func TestLoadRSAPrivateKey(t *testing.T) {
	g := NewWithT(t)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	g.Expect(err).To(Not(HaveOccurred()))

	pkcs1PEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	}))

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	g.Expect(err).To(Not(HaveOccurred()))
	pkcs8PEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	}))

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	g.Expect(err).To(Not(HaveOccurred()))
	ecPKCS8Bytes, err := x509.MarshalPKCS8PrivateKey(ecKey)
	g.Expect(err).To(Not(HaveOccurred()))
	ecPKCS8PEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: ecPKCS8Bytes,
	}))

	t.Run("ValidPKCS1", func(t *testing.T) {
		g := NewWithT(t)
		key, err := pkiutil.LoadRSAPrivateKey(pkcs1PEM)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(key).ToNot(BeNil())
		g.Expect(key.N).To(Equal(rsaKey.N))
	})

	t.Run("CorruptedPKCS1", func(t *testing.T) {
		g := NewWithT(t)
		badPEM := string(pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: []byte("invalid-pkcs1-bytes"),
		}))
		key, err := pkiutil.LoadRSAPrivateKey(badPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse RSA private key"))
		g.Expect(key).To(BeNil())
	})

	t.Run("ValidPKCS8", func(t *testing.T) {
		g := NewWithT(t)
		key, err := pkiutil.LoadRSAPrivateKey(pkcs8PEM)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(key).ToNot(BeNil())
		g.Expect(key.N).To(Equal(rsaKey.N))
	})

	t.Run("CorruptedPKCS8", func(t *testing.T) {
		g := NewWithT(t)
		badPEM := string(pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: []byte("invalid-pkcs8-bytes"),
		}))
		key, err := pkiutil.LoadRSAPrivateKey(badPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse private key"))
		g.Expect(key).To(BeNil())
	})

	t.Run("NonRSAPrivateKeyPKCS8", func(t *testing.T) {
		g := NewWithT(t)
		key, err := pkiutil.LoadRSAPrivateKey(ecPKCS8PEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not an RSA private key"))
		g.Expect(key).To(BeNil())
	})

	t.Run("InvalidPEMBlock", func(t *testing.T) {
		g := NewWithT(t)
		key, err := pkiutil.LoadRSAPrivateKey("plain text not pem")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse PEM block"))
		g.Expect(key).To(BeNil())
	})

	t.Run("UnknownBlockType", func(t *testing.T) {
		g := NewWithT(t)
		unknownPEM := string(pem.EncodeToMemory(&pem.Block{
			Type:  "UNKNOWN TYPE",
			Bytes: []byte("content"),
		}))
		key, err := pkiutil.LoadRSAPrivateKey(unknownPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unknown private key block type"))
		g.Expect(key).To(BeNil())
	})
}

func TestLoadRSAPublicKey(t *testing.T) {
	g := NewWithT(t)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	g.Expect(err).To(Not(HaveOccurred()))

	pubBytes, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	g.Expect(err).To(Not(HaveOccurred()))
	pubPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	}))

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	g.Expect(err).To(Not(HaveOccurred()))
	ecPubBytes, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	g.Expect(err).To(Not(HaveOccurred()))
	ecPubPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: ecPubBytes,
	}))

	t.Run("ValidRSAPublicKey", func(t *testing.T) {
		g := NewWithT(t)
		pub, err := pkiutil.LoadRSAPublicKey(pubPEM)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(pub).ToNot(BeNil())
		g.Expect(pub.N).To(Equal(rsaKey.PublicKey.N))
	})

	t.Run("NonRSAPublicKey", func(t *testing.T) {
		g := NewWithT(t)
		pub, err := pkiutil.LoadRSAPublicKey(ecPubPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not an RSA public key"))
		g.Expect(pub).To(BeNil())
	})

	t.Run("CorruptedPublicKeyBytes", func(t *testing.T) {
		g := NewWithT(t)
		badPEM := string(pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: []byte("corrupted-bytes"),
		}))
		pub, err := pkiutil.LoadRSAPublicKey(badPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse public key"))
		g.Expect(pub).To(BeNil())
	})

	t.Run("InvalidPEMBlock", func(t *testing.T) {
		g := NewWithT(t)
		pub, err := pkiutil.LoadRSAPublicKey("not pem")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse PEM block"))
		g.Expect(pub).To(BeNil())
	})

	t.Run("UnknownBlockType", func(t *testing.T) {
		g := NewWithT(t)
		badTypePEM := string(pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PUBLIC KEY",
			Bytes: []byte("content"),
		}))
		pub, err := pkiutil.LoadRSAPublicKey(badTypePEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unknown public key block type"))
		g.Expect(pub).To(BeNil())
	})
}

func TestLoadCertificateRequest(t *testing.T) {
	g := NewWithT(t)

	csrPEM, _, err := pkiutil.GenerateCSR(pkix.Name{CommonName: "test-csr"}, 2048, []string{"example.com"}, nil)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("ValidCSR", func(t *testing.T) {
		g := NewWithT(t)
		csr, err := pkiutil.LoadCertificateRequest(csrPEM)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(csr).ToNot(BeNil())
		g.Expect(csr.Subject.CommonName).To(Equal("test-csr"))
		g.Expect(csr.DNSNames).To(ConsistOf("example.com"))
	})

	t.Run("InvalidPEMBlock", func(t *testing.T) {
		g := NewWithT(t)
		csr, err := pkiutil.LoadCertificateRequest("not a pem")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse certificate request PEM"))
		g.Expect(csr).To(BeNil())
	})

	t.Run("UnknownBlockType", func(t *testing.T) {
		g := NewWithT(t)
		badPEM := string(pem.EncodeToMemory(&pem.Block{
			Type:  "OTHER REQUEST",
			Bytes: []byte("data"),
		}))
		csr, err := pkiutil.LoadCertificateRequest(badPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unknown certificate request block type"))
		g.Expect(csr).To(BeNil())
	})

	t.Run("CorruptedCSRBytes", func(t *testing.T) {
		g := NewWithT(t)
		corruptedPEM := string(pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE REQUEST",
			Bytes: []byte("garbage"),
		}))
		csr, err := pkiutil.LoadCertificateRequest(corruptedPEM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse certificate request"))
		g.Expect(csr).To(BeNil())
	})
}

func TestLoadCertificatePairFromDir(t *testing.T) {
	g := NewWithT(t)

	notBefore := time.Now().Add(-time.Hour)
	notAfter := notBefore.Add(24 * time.Hour)
	certPEM, keyPEM, err := pkiutil.GenerateSelfSignedCA(pkix.Name{CommonName: "test-pair"}, notBefore, notAfter, 2048)
	g.Expect(err).To(Not(HaveOccurred()))

	t.Run("Success", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		err := os.WriteFile(filepath.Join(dir, "mycert.crt"), []byte(certPEM), 0o600)
		g.Expect(err).To(Not(HaveOccurred()))
		err = os.WriteFile(filepath.Join(dir, "mycert.key"), []byte(keyPEM), 0o600)
		g.Expect(err).To(Not(HaveOccurred()))

		cert, key, err := pkiutil.LoadCertificatePairFromDir(dir, "mycert")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cert).ToNot(BeNil())
		g.Expect(cert.Subject.CommonName).To(Equal("test-pair"))
		g.Expect(key).ToNot(BeNil())
	})

	t.Run("MissingCertFile", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		cert, key, err := pkiutil.LoadCertificatePairFromDir(dir, "missing")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to read missing.crt"))
		g.Expect(cert).To(BeNil())
		g.Expect(key).To(BeNil())
	})

	t.Run("MissingKeyFile", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		err := os.WriteFile(filepath.Join(dir, "missingkey.crt"), []byte(certPEM), 0o600)
		g.Expect(err).To(Not(HaveOccurred()))

		cert, key, err := pkiutil.LoadCertificatePairFromDir(dir, "missingkey")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to read missingkey.key"))
		g.Expect(cert).To(BeNil())
		g.Expect(key).To(BeNil())
	})

	t.Run("InvalidCertContent", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		err := os.WriteFile(filepath.Join(dir, "bad.crt"), []byte("not a cert"), 0o600)
		g.Expect(err).To(Not(HaveOccurred()))
		err = os.WriteFile(filepath.Join(dir, "bad.key"), []byte(keyPEM), 0o600)
		g.Expect(err).To(Not(HaveOccurred()))

		cert, key, err := pkiutil.LoadCertificatePairFromDir(dir, "bad")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse bad certificate"))
		g.Expect(cert).To(BeNil())
		g.Expect(key).To(BeNil())
	})
}
