// Package keys generates proxy credentials with the operating system CSPRNG.
package keys

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

func random(n int) []byte { b := make([]byte, n); _, _ = rand.Read(b); return b }
func UUID() string {
	b := random(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func X25519() (priv, pub string) {
	b := random(32)
	b[0] &= 248
	b[31] &= 127
	b[31] |= 64
	priv = base64.RawURLEncoding.EncodeToString(b)
	pub, _ = PublicKey(priv)
	return
}
func PublicKey(priv string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != priv {
		return "", fmt.Errorf("Reality private key must be 32-byte unpadded base64url")
	}
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}
func ShortID() string    { return hex.EncodeToString(random(8)) }
func PSK16() string      { return base64.StdEncoding.EncodeToString(random(16)) }
func Password24() string { return base64.RawURLEncoding.EncodeToString(random(24)) }
func SubToken() string   { return base64.RawURLEncoding.EncodeToString(random(32)) }
