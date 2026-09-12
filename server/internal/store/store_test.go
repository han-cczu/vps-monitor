package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOpenAppliesPragmas(t *testing.T) {
	db := openTestDB(t)

	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode=%q want wal", journal)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys=%d want 1", fk)
	}
	var busy int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != 5000 {
		t.Fatalf("busy_timeout=%d want 5000", busy)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('users','settings','audit_log')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("tables=%d want 3", n)
	}
}

func TestUsers(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if n, _ := db.CountUsers(ctx); n != 0 {
		t.Fatalf("count=%d want 0", n)
	}
	if _, err := db.GetUserByUsername(ctx, "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	u, err := db.CreateUser(ctx, "admin", "hash1")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || u.CreatedAt == 0 {
		t.Fatalf("bad user %+v", u)
	}
	if _, err := db.CreateUser(ctx, "admin", "hash2"); err == nil {
		t.Fatal("duplicate username must fail")
	}

	got, err := db.GetUserByUsername(ctx, "admin")
	if err != nil || got.PasswordHash != "hash1" || got.TOTPEnabled || got.TOTPSecret != "" {
		t.Fatalf("GetUserByUsername: %+v err=%v", got, err)
	}
	byID, err := db.GetUserByID(ctx, u.ID)
	if err != nil || byID.Username != "admin" {
		t.Fatalf("GetUserByID: %+v err=%v", byID, err)
	}

	if err := db.UpdateUserPassword(ctx, u.ID, "hash3"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetUserByID(ctx, u.ID)
	if got.PasswordHash != "hash3" {
		t.Fatalf("password not updated: %s", got.PasswordHash)
	}
	if err := db.UpdateUserPassword(ctx, 999, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing user: %v", err)
	}
	if n, _ := db.CountUsers(ctx); n != 1 {
		t.Fatalf("count=%d want 1", n)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	type siteCfg struct {
		Title string `json:"title"`
		Unit  int    `json:"unit"`
	}

	var got siteCfg
	found, err := db.GetSetting(ctx, "site", &got)
	if err != nil || found {
		t.Fatalf("missing key: found=%v err=%v", found, err)
	}

	if err := db.SetSetting(ctx, "site", siteCfg{Title: "VPS Monitor", Unit: 1024}); err != nil {
		t.Fatal(err)
	}
	found, err = db.GetSetting(ctx, "site", &got)
	if err != nil || !found || got.Title != "VPS Monitor" || got.Unit != 1024 {
		t.Fatalf("roundtrip: found=%v err=%v got=%+v", found, err, got)
	}

	// 覆盖
	if err := db.SetSetting(ctx, "site", siteCfg{Title: "Renamed", Unit: 1000}); err != nil {
		t.Fatal(err)
	}
	_, _ = db.GetSetting(ctx, "site", &got)
	if got.Title != "Renamed" || got.Unit != 1000 {
		t.Fatalf("overwrite: %+v", got)
	}

	// 标量也行
	if err := db.SetSetting(ctx, "tz", "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	var tz string
	if _, err := db.GetSetting(ctx, "tz", &tz); err != nil || tz != "Asia/Shanghai" {
		t.Fatalf("scalar: %q err=%v", tz, err)
	}
}

func TestAudit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.InsertAudit(ctx, AuditEntry{TS: 100, Actor: "system", Action: "boot", TargetType: "server"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertAudit(ctx, AuditEntry{TS: 200, Actor: "admin", Action: "auth.password_change", TargetType: "user", TargetID: "1", Before: `{"a":1}`, After: `{"a":2}`, IP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	entries, err := db.ListAudit(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len=%d want 2", len(entries))
	}
	newest := entries[0]
	if newest.Action != "auth.password_change" || newest.Actor != "admin" || newest.TargetID != "1" ||
		newest.Before != `{"a":1}` || newest.After != `{"a":2}` || newest.IP != "127.0.0.1" {
		t.Fatalf("newest=%+v", newest)
	}
	oldest := entries[1]
	if oldest.TargetID != "" || oldest.Before != "" || oldest.After != "" || oldest.IP != "" {
		t.Fatalf("NULL columns must read back as empty strings: %+v", oldest)
	}

	// 分页
	page, _ := db.ListAudit(ctx, 1, 1)
	if len(page) != 1 || page[0].Action != "boot" {
		t.Fatalf("page=%+v", page)
	}
}
