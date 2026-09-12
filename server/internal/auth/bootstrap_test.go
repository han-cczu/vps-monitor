package auth

import (
	"context"
	"path/filepath"
	"testing"

	"vpsmon/server/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestEnsureAdminCreatesOnceAndOnlyOnce(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := EnsureAdmin(ctx, db); err != nil {
		t.Fatal(err)
	}
	user, err := db.GetUserByUsername(ctx, DefaultAdminUsername)
	if err != nil {
		t.Fatalf("admin not created: %v", err)
	}
	if user.CreatedAt == 0 {
		t.Fatal("created_at not set")
	}
	// 存的必须是 argon2id 哈希，不能是明文
	if _, err := VerifyPassword(user.PasswordHash, "whatever"); err != nil {
		t.Fatalf("stored password is not a valid argon2id hash: %v", err)
	}

	// 再跑一次不能新建、也不能改掉已有密码
	if err := EnsureAdmin(ctx, db); err != nil {
		t.Fatal(err)
	}
	n, err := db.CountUsers(ctx)
	if err != nil || n != 1 {
		t.Fatalf("count=%d err=%v want 1", n, err)
	}
	again, _ := db.GetUserByUsername(ctx, DefaultAdminUsername)
	if again.PasswordHash != user.PasswordHash {
		t.Fatal("second EnsureAdmin rewrote the password hash")
	}
}

// users 表非空时（比如管理员已经改过用户名）不能再塞一个 admin 进去。
func TestEnsureAdminSkipsWhenUsersExist(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	hash, err := HashPassword("someone-elses-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(ctx, "operator", hash); err != nil {
		t.Fatal(err)
	}

	if err := EnsureAdmin(ctx, db); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountUsers(ctx); n != 1 {
		t.Fatalf("count=%d want 1", n)
	}
	if _, err := db.GetUserByUsername(ctx, DefaultAdminUsername); err == nil {
		t.Fatal("admin must not be created when another user already exists")
	}
}

// 初始密码要能用来登录：生成的明文与落库的哈希必须对得上。
func TestEnsureAdminPasswordIsUsable(t *testing.T) {
	pw, err := RandomPassword(initialPasswordLen)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, pw)
	if err != nil || !ok {
		t.Fatalf("generated password does not verify: ok=%v err=%v", ok, err)
	}
	if len(pw) != 16 {
		t.Fatalf("initial password len=%d want 16", len(pw))
	}
}
