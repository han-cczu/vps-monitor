package certs

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGenerateCertificateAndFingerprint(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	c, err := Generate("www.bing.com", now)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject.CommonName != c.SNI || !slices.Equal(parsed.DNSNames, []string{c.SNI}) || parsed.IsCA || !parsed.BasicConstraintsValid || parsed.SerialNumber.BitLen() != 128 {
		t.Fatal("wrong certificate identity or constraints")
	}
	if !parsed.NotBefore.Equal(now.Add(-time.Hour)) || !parsed.NotAfter.Equal(now.Add(3650*24*time.Hour)) || parsed.NotAfter.Unix() != c.NotAfter {
		t.Fatal("wrong validity")
	}
	if parsed.KeyUsage != x509.KeyUsageDigitalSignature|x509.KeyUsageKeyAgreement || !slices.Equal(parsed.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Fatal("wrong key usages")
	}
	if k, ok := parsed.PublicKey.(*ecdsa.PublicKey); !ok || k.Params().BitSize != 256 {
		t.Fatal("not ECDSA P-256")
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	if _, err = parsed.Verify(x509.VerifyOptions{Roots: pool, DNSName: c.SNI, CurrentTime: now}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pair.Certificate[0])
	if strings.ReplaceAll(c.FingerprintSHA256, ":", "") != strings.ToUpper(hex.EncodeToString(sum[:])) {
		t.Fatal("incorrect fingerprint")
	}
	block, _ := pem.Decode([]byte(c.KeyPEM))
	if block.Type != "EC PRIVATE KEY" {
		t.Fatal("wrong private key PEM")
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), "PRIVATE KEY") || strings.Contains(string(b), "key_pem") {
		t.Fatal("private key leaked in public DTO")
	}
	c2, err := Generate(c.SNI, now)
	if err != nil || c2.FingerprintSHA256 == c.FingerprintSHA256 || c2.KeyPEM == c.KeyPEM {
		t.Fatal("regeneration did not rotate certificate")
	}
}
func TestInvalidSNI(t *testing.T) {
	for _, sni := range []string{"", "https://example.com", "example.com:443", "*.example.com", "-a.com", "a..com", "127.0.0.1", "[::1]", "x\ny.com"} {
		if _, err := Generate(sni, time.Now()); err == nil {
			t.Fatalf("accepted invalid SNI %q", sni)
		}
	}
}
func TestOpenSSL(t *testing.T) {
	tool, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("OpenSSL not installed on this platform")
	}
	c, err := Generate("cert-test.example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "cert.pem")
	if err = os.WriteFile(p, []byte(c.CertPEM), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(tool, "x509", "-in", p, "-noout", "-text", "-fingerprint", "-sha256").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "DNS:"+c.SNI) || !strings.Contains(string(out), c.FingerprintSHA256) {
		t.Fatalf("OpenSSL verification failed: %v %s", err, out)
	}
}
