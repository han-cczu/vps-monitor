package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// User 是 users 表的一行。TOTP 字段步骤 20 才会用到。
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	TOTPSecret   string // 未设置时为空串
	TOTPEnabled  bool
	CreatedAt    int64 // Unix 秒
}

const userColumns = "id, username, password_hash, COALESCE(totp_secret, ''), totp_enabled, created_at"

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (*User, error) {
	var (
		u           User
		totpEnabled int64
	)
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &totpEnabled, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.TOTPEnabled = totpEnabled != 0
	return &u, nil
}

// CountUsers 返回用户总数。
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CreateUser 新建用户，passwordHash 必须是 auth.HashPassword 的输出。
func (db *DB) CreateUser(ctx context.Context, username, passwordHash string) (*User, error) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)",
		username, passwordHash, now,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: now}, nil
}

// GetUserByUsername 按用户名查用户，没有则返回 ErrNotFound。
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username = ?", username))
}

// GetUserByID 按 ID 查用户，没有则返回 ErrNotFound。
func (db *DB) GetUserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ?", id))
}

// UpdateUserPassword 更新密码哈希，用户不存在返回 ErrNotFound。
func (db *DB) UpdateUserPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := db.ExecContext(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
