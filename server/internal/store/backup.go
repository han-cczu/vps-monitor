package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// BackupTo 把整个库备份到 path。
//
// 用 SQLite 的 VACUUM INTO 而不是拷文件：WAL 模式下 vm.db 旁边还有 -wal 和 -shm，
// 直接拷主文件会丢掉尚未 checkpoint 的事务，拷出来的可能是个损坏的库。
// VACUUM INTO 在一个事务里写出一份完整且已整理过的副本，服务端照常读写不受影响。
//
// path 必须不存在——这是 SQLite 的要求，不是本方法的额外限制。
func (db *DB) BackupTo(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("解析备份路径 %s: %w", path, err)
	}

	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("备份文件已存在: %s", abs)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查备份路径 %s: %w", abs, err)
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("创建备份目录: %w", err)
	}

	// VACUUM INTO 不接受占位符参数，路径只能拼进 SQL。
	// 这里的路径来自命令行而不是网络请求，且下面把单引号转义掉，注入面是封住的。
	quoted := "'" + escapeSQLString(abs) + "'"
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoted); err != nil {
		return fmt.Errorf("备份到 %s: %w", abs, err)
	}
	return nil
}

// escapeSQLString 按 SQL 字面量规则把单引号翻倍。
func escapeSQLString(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\'' {
			out = append(out, '\'')
		}
		out = append(out, r)
	}
	return string(out)
}
