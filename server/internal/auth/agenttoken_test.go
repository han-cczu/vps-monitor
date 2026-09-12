package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewAgentToken(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		token, err := NewAgentToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(token) != 43 { // 32 字节 base64url 无填充
			t.Fatalf("len=%d token=%q", len(token), token)
		}
		if strings.ContainsAny(token, "+/=") {
			t.Fatalf("token 不是 base64url：%q", token)
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(raw) != agentTokenBytes {
			t.Fatalf("decode %q: %d 字节 err=%v", token, len(raw), err)
		}
		if seen[token] {
			t.Fatalf("重复的 token：%q", token)
		}
		seen[token] = true
	}
}

func TestHashAgentToken(t *testing.T) {
	token, err := NewAgentToken()
	if err != nil {
		t.Fatal(err)
	}

	hash := HashAgentToken(token)
	if len(hash) != 64 { // sha256 hex
		t.Fatalf("len=%d hash=%q", len(hash), hash)
	}
	if hash == token || strings.Contains(hash, token) {
		t.Fatalf("哈希里能看到明文：%q", hash)
	}
	if HashAgentToken(token) != hash {
		t.Fatal("同一个 token 两次哈希结果不同")
	}
	// 首尾空白不影响：token 从配置文件读出来时常带换行
	if HashAgentToken(" "+token+"\n") != hash {
		t.Fatal("首尾空白改变了哈希结果")
	}

	other, err := NewAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if HashAgentToken(other) == hash {
		t.Fatal("不同 token 哈希相同")
	}
}
