// Package store 封装 SQLite 访问：打开、迁移，以及各表的 repo 方法（每张表一个文件，方法都带 ctx）。
//
// SQLite 是单写者：写操作走事务、靠 busy_timeout 排队；WAL 模式让读不被写阻塞。
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync/atomic"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，驱动名 "sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound 表示按主键 / 唯一键没查到记录。
var ErrNotFound = errors.New("store: not found")

// DB 是打开的数据库句柄，各表的方法都挂在它上面。
type DB struct {
	*sql.DB
	subscriptionEpoch atomic.Uint64
}

func (db *DB) SubscriptionEpoch() uint64 { return db.subscriptionEpoch.Load() }
func (db *DB) InvalidateSubscriptions()  { db.subscriptionEpoch.Add(1) }

// dsnParams 是每个连接建立时执行的 PRAGMA。busy_timeout 由驱动保证最先执行。
const dsnParams = "_pragma=busy_timeout(5000)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=foreign_keys(1)" +
	"&_pragma=synchronous(NORMAL)"

// Open 打开（不存在则创建）path 指向的 SQLite 数据库。
//
// 连接池上限 4：SQLite 只有一个写者，多余的连接只会在 busy_timeout 里排队。
// 注意不要传 ":memory:"，连接池里的每个连接会各自得到一个空库。
func Open(path string) (*DB, error) {
	// 这里故意不用 file: URI 形式：Windows 路径里的反斜杠、空格在 URI 里都要转义，
	// 而驱动对"普通路径 + ?query"同样会解析 _pragma。
	dsn := path + "?" + dsnParams

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	return &DB{DB: sqlDB}, nil
}

// Migrate 用 goose 把 migrations/ 下的 SQL 迁移执行到最新版本。重复执行是幂等的。
func (db *DB) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return err
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, db.DB, sub, goose.WithSlog(slog.Default()))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		slog.Info("migration applied",
			"version", r.Source.Version,
			"file", filepath.Base(r.Source.Path),
			"duration", r.Duration.String(),
		)
	}
	return nil
}

// WithTx 在一个事务里执行 fn；fn 返回错误则回滚，否则提交。
func (db *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
