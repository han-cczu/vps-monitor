package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashPasswordFormatAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected hash prefix: %s", hash)
	}

	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("correct password: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "correct horse battery stapl")
	if err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "")
	if err != nil || ok {
		t.Fatalf("empty password: ok=%v err=%v", ok, err)
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

// 用固定盐手工拼一个 PHC 串，验证参数解析走的是串里的值而不是常量。
func TestVerifyPasswordParsesParamsFromHash(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("pw"), salt, 2, 8*1024, 1, 16) // 与默认 t/m/p/len 全都不同
	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", 8*1024, 2, 1,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))

	ok, err := VerifyPassword(encoded, "pw")
	if err != nil || !ok {
		t.Fatalf("custom params: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(encoded, "pW")
	if err != nil || ok {
		t.Fatalf("custom params wrong pw: ok=%v err=%v", ok, err)
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	good, _ := HashPassword("x")
	parts := strings.Split(good, "$")

	cases := map[string]string{
		"empty":            "",
		"plain":            "not-a-hash",
		"wrong algo":       strings.Replace(good, "argon2id", "argon2i", 1),
		"wrong version":    strings.Replace(good, "v=19", "v=18", 1),
		"missing segment":  strings.Join(parts[:5], "$"),
		"bad salt b64":     strings.Join(append(append([]string{}, parts[:4]...), "!!!", parts[5]), "$"),
		"bad hash b64":     strings.Join(append(append([]string{}, parts[:5]...), "!!!"), "$"),
		"zero memory":      strings.Replace(good, "m=65536", "m=0", 1),
		"huge memory":      strings.Replace(good, "m=65536", "m=99999999", 1),
		"garbage params":   strings.Replace(good, "m=65536,t=3,p=2", "m=a,t=b,p=c", 1),
		"empty hash bytes": strings.Join(append(append([]string{}, parts[:5]...), ""), "$"),
	}
	for name, encoded := range cases {
		ok, err := VerifyPassword(encoded, "x")
		if ok || !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%s: want ErrInvalidHash, got ok=%v err=%v", name, ok, err)
		}
	}
}

func TestVerifyDummyDoesNotPanic(t *testing.T) {
	VerifyDummy("anything")
	VerifyDummy("")
}

func TestRandomPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		pw, err := RandomPassword(16)
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 16 {
			t.Fatalf("len=%d", len(pw))
		}
		for _, c := range pw {
			if !strings.ContainsRune(passwordAlphabet, c) {
				t.Fatalf("unexpected char %q in %s", c, pw)
			}
		}
		if seen[pw] {
			t.Fatalf("duplicate password generated: %s", pw)
		}
		seen[pw] = true
	}
}
