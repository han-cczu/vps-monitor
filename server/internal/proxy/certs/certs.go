package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

type Cert struct {
	ServerID          int64  `json:"server_id"`
	SNI               string `json:"sni"`
	CertPEM           string `json:"cert_pem"`
	KeyPEM            string `json:"-"`
	FingerprintSHA256 string `json:"fingerprint_sha256"`
	NotAfter          int64  `json:"not_after"`
	CreatedAt         int64  `json:"created_at"`
}

// ValidSNI accepts DNS names, without scheme, port, wildcard or IP literals.
func ValidSNI(s string) bool {
	if len(s) == 0 || len(s) > 253 || !strings.Contains(s, ".") || net.ParseIP(s) != nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func Generate(sni string, now time.Time) (Cert, error) {
	if !ValidSNI(sni) {
		return Cert{}, fmt.Errorf("SNI 必须是合法的 DNS 域名")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Cert{}, err
	}
	serialBytes := make([]byte, 16)
	_, _ = rand.Read(serialBytes)
	serialBytes[0] |= 0x80
	template := &x509.Certificate{SerialNumber: new(big.Int).SetBytes(serialBytes), Subject: pkix.Name{CommonName: sni}, DNSNames: []string{sni},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(3650 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return Cert{}, err
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Cert{}, err
	}
	hash := sha256.Sum256(der)
	parts := make([]string, len(hash))
	for i, b := range hash {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return Cert{SNI: sni, CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})),
		FingerprintSHA256: strings.Join(parts, ":"), NotAfter: template.NotAfter.Unix(), CreatedAt: now.Unix()}, nil
}
