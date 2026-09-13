package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ----------------------------------------------------------------------

func TestBackupToProducesUsableCopy(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.CreateUser(ctx, "admin", "hash"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "backup", "vm.db")
	if err := db.BackupTo(ctx, path); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("备份文件是空的")
	}

	// 备份出来的必须是一个能直接打开、数据齐全的库
	restored, err := Open(path)
	if err != nil {
		t.Fatalf("打开备份: %v", err)
	}
	defer restored.Close()

	user, err := restored.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("备份里读不到用户: %v", err)
	}
	if user.PasswordHash != "hash" {
		t.Errorf("备份里的数据不对: %+v", user)
	}
}

// TestBackupToRefusesExistingFile 覆盖 SQLite 的硬性要求：VACUUM INTO 的目标必须不存在。
// 提前检查是为了给出一句人话，而不是让调用方看到一句 SQL 错误。
func TestBackupToRefusesExistingFile(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	path := filepath.Join(t.TempDir(), "vm.db")
	if err := os.WriteFile(path, []byte("占位"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := db.BackupTo(ctx, path); err == nil {
		t.Fatal("目标已存在时应当报错")
	}

	// 原文件不能被动过
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "占位" {
		t.Errorf("失败时不该覆盖已有文件，现在是 %q / %v", raw, err)
	}
}

// TestBackupToCreatesParentDir：第一次备份时 {DataDir}/backup 还不存在。
func TestBackupToCreatesParentDir(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	path := filepath.Join(t.TempDir(), "a", "b", "vm.db")
	if err := db.BackupTo(ctx, path); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("应当自动建好上级目录: %v", err)
	}
}

func TestEscapeSQLString(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`/data/backup/vm.db`, `/data/backup/vm.db`},
		{`/data/it's/vm.db`, `/data/it''s/vm.db`},
		{`''`, `''''`},
		{`C:\Users\diao\vm.db`, `C:\Users\diao\vm.db`},
	}

	for _, tc := range tests {
		if got := escapeSQLString(tc.in); got != tc.want {
			t.Errorf("escapeSQLString(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}
