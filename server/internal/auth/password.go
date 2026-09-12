// Package auth 放认证相关的纯逻辑：argon2id 密码、JWT 签发校验、登录限速、请求上下文里的当前用户。
//
// HTTP handler 不在这里（见 api 包），这样 audit 包可以引用 auth 而不形成循环依赖。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// argon2id 参数：t=3、m=64 MiB、p=2、32 字节输出。校验时以哈希串里记录的参数为准，将来调参不影响旧密码。
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16

	// 校验时对哈希串里参数的上限，防止被篡改的哈希把进程内存打爆。
	maxArgonMemory  = 1 << 20 // 1 GiB
	maxArgonTime    = 64
	maxArgonThreads = 16
)

// ErrInvalidHash 表示密码哈希串不是合法的 argon2id PHC 格式。
var ErrInvalidHash = errors.New("auth: 密码哈希格式不合法")

// HashPassword 用 argon2id 哈希密码，输出 PHC 格式：
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt b64>$<hash b64>
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword 校验密码是否与 encoded 哈希匹配。格式不合法返回 ErrInvalidHash。
func VerifyPassword(encoded, password string) (bool, error) {
	// ["", "argon2id", "v=19", "m=65536,t=3,p=2", salt, hash]
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}

	var (
		memory, timeCost uint32
		threads          uint8
	)
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, ErrInvalidHash
	}
	if memory == 0 || memory > maxArgonMemory || timeCost == 0 || timeCost > maxArgonTime || threads == 0 || threads > maxArgonThreads {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy 对一个固定的假哈希做一次校验，让"用户不存在"与"密码错误"的耗时接近，
// 避免通过响应时间探测用户名是否存在。
func VerifyDummy(password string) {
	dummyOnce.Do(func() {
		dummyHash, _ = HashPassword("vps-monitor-dummy-password-for-timing")
	})
	_, _ = VerifyPassword(dummyHash, password)
}
