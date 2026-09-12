package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTTL 是访问令牌的有效期（设计方案 §6.7：12 小时）。
const TokenTTL = 12 * time.Hour

// RoleAdmin 是目前唯一的角色。
const RoleAdmin = "admin"

// MinSecretLen 是 HS256 密钥的最小长度（字节）。太短的密钥可以被离线穷举。
const MinSecretLen = 32

// ErrInvalidToken 表示 token 缺失、签名不对、已过期或内容不合法。
var ErrInvalidToken = errors.New("auth: token 无效")

// Principal 是 token 里携带、放进请求上下文的当前用户。
type Principal struct {
	ID   int64
	Name string
	Role string
}

// Claims 是 JWT 载荷：sub 放用户 ID，另带 name 与 role。
type Claims struct {
	Name string `json:"name"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Tokens 负责签发与校验 HS256 JWT。
type Tokens struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewTokens 创建签发器。secret 长度不足 MinSecretLen 时报错。
func NewTokens(secret string, ttl time.Duration) (*Tokens, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("auth: JWT 密钥至少 %d 字节，当前 %d 字节", MinSecretLen, len(secret))
	}
	if ttl <= 0 {
		return nil, errors.New("auth: token 有效期必须大于 0")
	}
	return &Tokens{secret: []byte(secret), ttl: ttl, now: time.Now}, nil
}

// Issue 为 p 签发一个 token，返回 token 与过期时刻。
func (t *Tokens) Issue(p Principal) (string, time.Time, error) {
	now := t.now()
	exp := now.Add(t.ttl)
	claims := Claims{
		Name: p.Name,
		Role: p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(p.ID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// Parse 校验 token（签名算法、签名、exp）并还原 Principal。
// 过期时返回的错误同时满足 errors.Is(err, ErrInvalidToken) 与 errors.Is(err, jwt.ErrTokenExpired)。
func (t *Tokens) Parse(token string) (Principal, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 {
		return Principal{}, fmt.Errorf("%w: sub 不是合法的用户 ID", ErrInvalidToken)
	}
	return Principal{ID: id, Name: claims.Name, Role: claims.Role}, nil
}
