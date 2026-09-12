package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// agentTokenBytes 是 agent token 的随机字节数：32 字节 → base64url 43 个字符。
const agentTokenBytes = 32

// NewAgentToken 生成一个 agent token 明文。
//
// token 只在创建与重置节点时返回一次，库里只存 HashAgentToken 的结果；
// 丢了就只能重置，不能找回。
func NewAgentToken() (string, error) {
	raw := make([]byte, agentTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成 agent token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashAgentToken 返回 token 的 sha256 hex，用于入库与比对。
//
// 这里不用 argon2：token 是 32 字节随机数，没有穷举空间，
// 而 agent 每次重连都要校验一次，用慢哈希只会白烧 CPU。
func HashAgentToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}
