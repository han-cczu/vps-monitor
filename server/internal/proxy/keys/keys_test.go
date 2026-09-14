package keys

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"testing"
)

func TestCredentialFormatsAndUniqueness(t *testing.T) {
	uuidRE := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 128 {
		u := UUID()
		if !uuidRE.MatchString(u) || seen[u] {
			t.Fatal("invalid or repeated UUID")
		}
		seen[u] = true
		for _, c := range []struct {
			s        string
			n        int
			encoding *base64.Encoding
		}{{PSK16(), 16, base64.StdEncoding}, {Password24(), 24, base64.RawURLEncoding}, {SubToken(), 32, base64.RawURLEncoding}} {
			b, err := c.encoding.DecodeString(c.s)
			if err != nil || len(b) != c.n || seen[c.s] {
				t.Fatal("invalid or repeated random credential")
			}
			seen[c.s] = true
		}
		id := ShortID()
		b, err := hex.DecodeString(id)
		if err != nil || len(b) != 8 || seen[id] {
			t.Fatal("invalid or repeated short ID")
		}
		seen[id] = true
		priv, pub := X25519()
		b, err = base64.RawURLEncoding.DecodeString(priv)
		if err != nil || len(b) != 32 || b[0]&7 != 0 || b[31]&128 != 0 || b[31]&64 == 0 {
			t.Fatal("private key clamp or encoding incorrect")
		}
		key, err := ecdh.X25519().NewPrivateKey(b)
		if err != nil {
			t.Fatal(err)
		}
		if base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) != pub || seen[pub] {
			t.Fatal("public key derivation or uniqueness failed")
		}
		seen[pub] = true
	}
}
