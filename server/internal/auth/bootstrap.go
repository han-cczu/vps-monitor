package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"

	"vpsmon/server/internal/store"
)

// DefaultAdminUsername 是首次启动自动创建的管理员用户名。
const DefaultAdminUsername = "admin"

const initialPasswordLen = 16

// EnsureAdmin 在 users 表为空时创建初始管理员。随机密码只在这里的日志里打印一次，
// 之后既不落盘也查不回来；忘了就按 runbook 删掉该用户重启重建。
func EnsureAdmin(ctx context.Context, db *store.DB) error {
	n, err := db.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if n > 0 {
		return nil
	}

	password, err := RandomPassword(initialPasswordLen)
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := db.CreateUser(ctx, DefaultAdminUsername, hash); err != nil {
		return fmt.Errorf("create admin: %w", err)
	}

	slog.Warn("initial admin password (printed only once)",
		"username", DefaultAdminUsername,
		"password", password,
	)
	return nil
}

const passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// RandomPassword 生成 n 位由大小写字母与数字组成的随机密码。
func RandomPassword(n int) (string, error) {
	out := make([]byte, n)
	alphabetLen := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", fmt.Errorf("生成随机密码: %w", err)
		}
		out[i] = passwordAlphabet[idx.Int64()]
	}
	return string(out), nil
}
