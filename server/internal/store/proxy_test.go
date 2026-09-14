package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestProxyMigrationUpgradeRollbackAndReplay(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.DB, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	node, err := db.CreateServer(ctx, ServerInput{Name: "pre-proxy-node"}, "existing-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	core, err := db.Proxy().Core(ctx, node.ID)
	if err != nil || core.ServerID != node.ID || core.Core != "sing-box" {
		t.Fatal("existing node not backfilled")
	}
	existing, err := db.GetServer(ctx, node.ID)
	if err != nil || existing.Name != "pre-proxy-node" || existing.TokenHash != "existing-token" {
		t.Fatal("upgrade damaged existing node")
	}
	if _, err = provider.DownTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if _, err = db.GetServer(ctx, node.ID); err != nil {
		t.Fatal("proxy rollback removed node")
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal("cannot reapply migration", err)
	}
	if _, err = db.Proxy().Core(ctx, node.ID); err != nil {
		t.Fatal("replay did not backfill node")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
}

func TestPortTriggersProtectDirectWriters(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "ports.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	node, err := db.CreateServer(ctx, ServerInput{Name: "node"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	insert := func(tag, protocol string, port int) error {
		_, err := db.ExecContext(ctx, `INSERT INTO inbounds(server_id,tag,protocol,listen_port,settings,created_at,updated_at) VALUES(?,?,?,?,'{}',1,1)`, node.ID, tag, protocol, port)
		return err
	}
	if err = insert("ss", "shadowsocks", 443); err != nil {
		t.Fatal(err)
	}
	if err = insert("tuic", "tuic", 443); err == nil || !strings.Contains(err.Error(), "proxy_port_conflict") {
		t.Fatalf("direct insert bypassed port guard: %v", err)
	}
	if err = insert("hy2", "hysteria2", 444); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE inbounds SET listen_port=443 WHERE tag='hy2'`); err == nil || !strings.Contains(err.Error(), "proxy_port_conflict") {
		t.Fatalf("direct update bypassed port guard: %v", err)
	}
}

func TestImmediateTransactionCancellationReleasesConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	err = db.WithProxyTx(ctx, func(q ProxyQueries) error {
		if _, err := q.DB.ExecContext(ctx, `INSERT INTO settings VALUES('cancel-test','1')`); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wrong error: %v", err)
	}
	err = db.WithProxyTx(context.Background(), func(q ProxyQueries) error {
		var count int
		if err := q.DB.QueryRowContext(context.Background(), `SELECT count(*) FROM settings WHERE key='cancel-test'`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Error("cancelled transaction committed")
		}
		return nil
	})
	if err != nil {
		t.Fatal("connection left in transaction", err)
	}
}
