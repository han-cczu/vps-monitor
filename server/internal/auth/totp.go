package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
)

var ErrMFA = errors.New("验证码无效、已使用或凭据已过期，请重新登录")

const TicketTTL = 5 * time.Minute

type challenge struct {
	UserID       int64
	PasswordHash string
	Expires      time.Time
}
type enrollment struct {
	PasswordHash string
	Secret       string
	Expires      time.Time
}

// MFA holds only short-lived challenges. Accepted TOTP steps are stored in SQLite.
type MFA struct {
	mu      sync.Mutex
	tickets map[string]challenge
	pending map[int64]enrollment
	aead    cipher.AEAD
	now     func() time.Time
}

func (t *Tokens) NewMFA() *MFA {
	mac := hmac.New(sha256.New, t.secret)
	mac.Write([]byte("vps-monitor/totp/aes-gcm/v1"))
	block, _ := aes.NewCipher(mac.Sum(nil))
	aead, _ := cipher.NewGCM(block)
	return &MFA{tickets: make(map[string]challenge), pending: make(map[int64]enrollment), aead: aead, now: time.Now}
}
func (m *MFA) Encrypt(id int64, secret string) (string, error) {
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := m.aead.Seal(nonce, nonce, []byte(secret), []byte("totp:"+strconv.FormatInt(id, 10)))
	return "v1:" + base64.RawStdEncoding.EncodeToString(out), nil
}
func (m *MFA) Decrypt(id int64, encrypted string) (string, error) {
	if len(encrypted) < 3 || encrypted[:3] != "v1:" {
		return "", ErrMFA
	}
	raw, err := base64.RawStdEncoding.DecodeString(encrypted[3:])
	if err != nil || len(raw) < m.aead.NonceSize() {
		return "", ErrMFA
	}
	plain, err := m.aead.Open(nil, raw[:m.aead.NonceSize()], raw[m.aead.NonceSize():], []byte("totp:"+strconv.FormatInt(id, 10)))
	if err != nil {
		return "", ErrMFA
	}
	return string(plain), nil
}
func (m *MFA) prune() {
	now := m.now()
	for k, v := range m.tickets {
		if !now.Before(v.Expires) {
			delete(m.tickets, k)
		}
	}
	for k, v := range m.pending {
		if !now.Before(v.Expires) {
			delete(m.pending, k)
		}
	}
}
func (m *MFA) Ticket(id int64, passwordHash string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune()
	for k, v := range m.tickets {
		if v.UserID == id {
			delete(m.tickets, k)
		}
	}
	if len(m.tickets) >= 1024 {
		return "", errors.New("MFA capacity exceeded")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	m.tickets[token] = challenge{id, passwordHash, m.now().Add(TicketTTL)}
	return token, nil
}

// Consume burns the ticket even when the subsequent code is wrong: no retry oracle.
func (m *MFA) Consume(ticket string) (int64, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune()
	c, ok := m.tickets[ticket]
	delete(m.tickets, ticket)
	if !ok {
		return 0, "", ErrMFA
	}
	return c.UserID, c.PasswordHash, nil
}
func (m *MFA) Setup(id int64, username, passwordHash string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "VPS Monitor", AccountName: username, SecretSize: 20})
	if err != nil {
		return "", "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune()
	if len(m.pending) >= 1024 {
		return "", "", errors.New("MFA capacity exceeded")
	}
	m.pending[id] = enrollment{PasswordHash: passwordHash, Secret: key.Secret(), Expires: m.now().Add(10 * time.Minute)}
	return key.Secret(), key.URL(), nil
}
func (m *MFA) Pending(id int64, passwordHash string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune()
	v, ok := m.pending[id]
	if !ok || v.PasswordHash != passwordHash {
		return "", ErrMFA
	}
	return v.Secret, nil
}
func (m *MFA) Clear(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, id)
	for k, v := range m.tickets {
		if v.UserID == id {
			delete(m.tickets, k)
		}
	}
}

// Match returns the actual matching step, including +/- 30s drift, for persistent replay prevention.
func MatchTOTP(code, secret string, now time.Time) (int64, error) {
	if len(code) != 6 {
		return 0, ErrMFA
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, ErrMFA
		}
	}
	for _, delta := range []int64{0, -1, 1} {
		step := now.Unix()/30 + delta
		expected, err := totp.GenerateCode(secret, time.Unix(step*30, 0))
		if err != nil {
			return 0, fmt.Errorf("generate TOTP: %w", err)
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return step, nil
		}
	}
	return 0, ErrMFA
}
