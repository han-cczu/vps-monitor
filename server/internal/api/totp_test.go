package api

import (
	"context"
	"github.com/pquerna/otp/totp"
	"net/http"
	"strings"
	"testing"
	"time"
	"vpsmon/server/internal/auth"
)

func TestTOTPEnrollmentMFAAndPersistentReplay(t *testing.T) {
	var m *auth.MFA
	e := newTestEnv(t, func(d *Deps) { m = d.Tokens.NewMFA(); d.MFA = m })
	token := e.adminToken(t)
	for _, path := range []string{"/api/auth/totp/setup", "/api/auth/totp/enable", "/api/auth/totp/disable"} {
		resp, _ := e.do(t, "POST", path, "", map[string]string{})
		if resp.StatusCode != 401 {
			t.Fatalf("unprotected %s", path)
		}
	}
	resp, body := e.do(t, "POST", "/api/auth/totp/setup", token, map[string]string{"password": testPassword})
	if resp.StatusCode != 200 {
		t.Fatal(body)
	}
	secret := body["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now())
	resp, body = e.do(t, "POST", "/api/auth/totp/enable", token, map[string]string{"code": code})
	if resp.StatusCode != 204 {
		t.Fatal(body)
	}
	u, _ := e.db.GetUserByUsername(context.Background(), "admin")
	if !u.TOTPEnabled || strings.Contains(u.TOTPSecret, secret) {
		t.Fatal("not encrypted/enabled")
	}
	resp, body = e.signIn(t, "admin", testPassword)
	if resp.StatusCode != 200 || body["mfaRequired"] != true || body["accessToken"] != nil {
		t.Fatal(body)
	}
	ticket := body["ticket"].(string)
	resp, _ = e.do(t, "POST", "/api/auth/mfa", "", map[string]string{"ticket": ticket, "code": code})
	if resp.StatusCode != 401 {
		t.Fatal("enrollment code replayed")
	}
	// A different accepted time step succeeds without sleeping; tests use the allowed drift window.
	next, _ := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	_, body = e.signIn(t, "admin", testPassword)
	ticket = body["ticket"].(string)
	resp, body = e.do(t, "POST", "/api/auth/mfa", "", map[string]string{"ticket": ticket, "code": next})
	if resp.StatusCode != 200 || body["accessToken"] == nil {
		t.Fatalf("MFA %d %v", resp.StatusCode, body)
	}
	resp, _ = e.do(t, "POST", "/api/auth/mfa", "", map[string]string{"ticket": ticket, "code": next})
	if resp.StatusCode != 401 {
		t.Fatal("ticket reused")
	}
	// SQLite rejects reuse independent of the in-memory ticket lifecycle.
	if err := e.db.ConsumeTOTP(context.Background(), u, time.Now().Unix()/30+1, next, time.Now()); err == nil {
		t.Fatal("persistent replay accepted")
	}
	entries, _ := e.db.ListAudit(context.Background(), 20, 0)
	for _, entry := range entries {
		if strings.Contains(entry.After, secret) || strings.Contains(entry.Before, secret) {
			t.Fatal("secret in audit")
		}
	}
	if err := e.db.ResetTOTP(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = e.signIn(t, "admin", testPassword)
	if resp.StatusCode != http.StatusOK || body["accessToken"] == nil {
		t.Fatal("reset did not restore password login")
	}
}
func TestMFABruteForceLimitSurvivesFreshPassword(t *testing.T) {
	var m *auth.MFA
	e := newTestEnv(t, func(d *Deps) { m = d.Tokens.NewMFA(); d.MFA = m })
	u, _ := e.db.GetUserByUsername(context.Background(), "admin")
	s, _ := m.Encrypt(u.ID, "JBSWY3DPEHPK3PXP")
	if err := e.db.SetTOTP(context.Background(), u.ID, "", s, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		_, b := e.signIn(t, "admin", testPassword)
		ticket, _ := b["ticket"].(string)
		resp, _ := e.do(t, "POST", "/api/auth/mfa", "", map[string]string{"ticket": ticket, "code": "x"})
		want := 401
		if i == 5 {
			want = 429
		}
		if resp.StatusCode != want {
			t.Fatalf("attempt %d status %d", i, resp.StatusCode)
		}
	}
}

func TestTOTPEnrollmentInvalidatedByPasswordReset(t *testing.T) {
	e := newTestEnv(t)
	token := e.adminToken(t)
	resp, body := e.do(t, "POST", "/api/auth/totp/setup", token, map[string]string{"password": testPassword})
	if resp.StatusCode != 200 {
		t.Fatal(body)
	}
	secret := body["secret"].(string)
	u, _ := e.db.GetUserByUsername(context.Background(), "admin")
	// Direct DB reset represents the separate reset-password CLI process: it cannot clear memory.
	if err := e.db.UpdateUserPassword(context.Background(), u.ID, "changed password hash"); err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	resp, body = e.do(t, "POST", "/api/auth/totp/enable", token, map[string]string{"code": code})
	if resp.StatusCode != 400 {
		t.Fatalf("old setup accepted after password reset %d %v", resp.StatusCode, body)
	}
	u, _ = e.db.GetUserByID(context.Background(), u.ID)
	if u.TOTPEnabled {
		t.Fatal("MFA enabled by stale enrollment")
	}
}

func TestMFADatabaseFailureDoesNotLockAccount(t *testing.T) {
	var m *auth.MFA
	e := newTestEnv(t, func(d *Deps) { m = d.Tokens.NewMFA(); d.MFA = m })
	u, err := e.db.GetUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.db.Close(); err != nil {
		t.Fatal(err)
	}
	// Repeated infrastructure faults must not become credential failures/429.
	for i := 0; i < 6; i++ {
		ticket, err := m.Ticket(u.ID, u.PasswordHash)
		if err != nil {
			t.Fatal(err)
		}
		resp, _ := e.do(t, "POST", "/api/auth/mfa", "", map[string]string{"ticket": ticket, "code": "123456"})
		if resp.StatusCode != 500 {
			t.Fatalf("database fault %d returned %d", i, resp.StatusCode)
		}
	}
}
