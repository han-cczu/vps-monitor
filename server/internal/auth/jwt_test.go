package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef" // 32 字节

func newTestTokens(t *testing.T, now time.Time) *Tokens {
	t.Helper()
	tk, err := NewTokens(testSecret, TokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	tk.now = func() time.Time { return now }
	return tk
}

func TestNewTokensRejectsShortSecret(t *testing.T) {
	if _, err := NewTokens("short", TokenTTL); err == nil {
		t.Fatal("short secret must be rejected")
	}
	if _, err := NewTokens(testSecret, 0); err == nil {
		t.Fatal("zero ttl must be rejected")
	}
}

func TestIssueAndParse(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	tk := newTestTokens(t, now)

	want := Principal{ID: 7, Name: "admin", Role: RoleAdmin}
	token, exp, err := tk.Issue(want)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(now.Add(TokenTTL)) {
		t.Fatalf("exp=%v want %v", exp, now.Add(TokenTTL))
	}

	got, err := tk.Parse(token)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestParseExpired(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	tk := newTestTokens(t, now)
	token, _, _ := tk.Issue(Principal{ID: 1, Name: "admin", Role: RoleAdmin})

	// 11h59m 还有效
	tk.now = func() time.Time { return now.Add(TokenTTL - time.Minute) }
	if _, err := tk.Parse(token); err != nil {
		t.Fatalf("token should still be valid: %v", err)
	}

	// 12h01m 过期
	tk.now = func() time.Time { return now.Add(TokenTTL + time.Minute) }
	_, err := tk.Parse(token)
	if err == nil {
		t.Fatal("expired token accepted")
	}
	if !errors.Is(err, ErrInvalidToken) || !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("want ErrInvalidToken + jwt.ErrTokenExpired, got %v", err)
	}
}

func TestParseRejectsWrongSecretAndTampering(t *testing.T) {
	now := time.Now()
	tk := newTestTokens(t, now)
	token, _, _ := tk.Issue(Principal{ID: 1, Name: "admin", Role: RoleAdmin})

	other, _ := NewTokens("ffffffffffffffffffffffffffffffff", TokenTTL)
	if _, err := other.Parse(token); err == nil {
		t.Fatal("token signed with another secret accepted")
	}

	// 篡改载荷
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + parts[1] + "x." + parts[2]
	if _, err := tk.Parse(tampered); err == nil {
		t.Fatal("tampered token accepted")
	}

	// alg=none
	none := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{Name: "admin", Role: RoleAdmin,
		RegisteredClaims: jwt.RegisteredClaims{Subject: "1", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}})
	noneToken, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tk.Parse(noneToken); err == nil {
		t.Fatal("alg=none token accepted")
	}

	// 没有 exp
	noExp := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{Name: "admin", Role: RoleAdmin,
		RegisteredClaims: jwt.RegisteredClaims{Subject: "1"}})
	noExpToken, _ := noExp.SignedString([]byte(testSecret))
	if _, err := tk.Parse(noExpToken); err == nil {
		t.Fatal("token without exp accepted")
	}

	// sub 不是数字
	badSub := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{Name: "admin", Role: RoleAdmin,
		RegisteredClaims: jwt.RegisteredClaims{Subject: "abc", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}})
	badSubToken, _ := badSub.SignedString([]byte(testSecret))
	if _, err := tk.Parse(badSubToken); err == nil {
		t.Fatal("token with non-numeric sub accepted")
	}
}

func TestMiddleware(t *testing.T) {
	tk := newTestTokens(t, time.Now())
	token, _, _ := tk.Issue(Principal{ID: 42, Name: "admin", Role: RoleAdmin})

	var gotPrincipal Principal
	var gotOK bool
	h := tk.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal, gotOK = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		header string
		status int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic " + token, http.StatusUnauthorized},
		{"garbage", "Bearer nope", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"ok", "Bearer " + token, http.StatusOK},
		{"ok lowercase scheme", "bearer " + token, http.StatusOK},
	}
	for _, c := range cases {
		gotOK = false
		req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != c.status {
			t.Errorf("%s: status=%d want %d", c.name, rec.Code, c.status)
		}
		if c.status == http.StatusUnauthorized {
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("%s: content-type=%q", c.name, ct)
			}
			if !strings.Contains(rec.Body.String(), `"message":"unauthorized"`) {
				t.Errorf("%s: body=%s", c.name, rec.Body.String())
			}
		} else if !gotOK || gotPrincipal.ID != 42 || gotPrincipal.Name != "admin" {
			t.Errorf("%s: principal not in context: %+v ok=%v", c.name, gotPrincipal, gotOK)
		}
	}
}
