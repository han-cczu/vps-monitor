package auth

import (
	"testing"
	"time"
)

func TestMFAEncryptionTicketAndReplayStep(t *testing.T) {
	tokens, _ := NewTokens("01234567890123456789012345678901", TokenTTL)
	m := tokens.NewMFA()
	encrypted, err := m.Encrypt(1, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if s, err := m.Decrypt(1, encrypted); err != nil || s != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("decrypt %s %v", s, err)
	}
	if _, err = m.Decrypt(2, encrypted); err == nil {
		t.Fatal("AAD substitution accepted")
	}
	other, _ := NewTokens("11234567890123456789012345678901", TokenTTL)
	if _, err = other.NewMFA().Decrypt(1, encrypted); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err = m.Decrypt(1, encrypted[:len(encrypted)-3]+"xxx"); err == nil {
		t.Fatal("tampering accepted")
	}
	now := time.Now()
	m.now = func() time.Time { return now }
	ticket, _ := m.Ticket(1, "hash")
	if id, _, err := m.Consume(ticket); err != nil || id != 1 {
		t.Fatal(err)
	}
	if _, _, err := m.Consume(ticket); err == nil {
		t.Fatal("ticket reused")
	}
	ticket, _ = m.Ticket(1, "hash")
	now = now.Add(TicketTTL)
	if _, _, err := m.Consume(ticket); err == nil {
		t.Fatal("expired ticket accepted")
	}
	// RFC 6238 SHA1 secret/test vector, truncated to six digits.
	if step, err := MatchTOTP("287082", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0)); err != nil || step != 1 {
		t.Fatalf("RFC vector %d %v", step, err)
	}
}
