package maintenance

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"vpsmon/server/internal/store"
)

func TestMaintenanceRetentionAndCheckpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_ = db.SetSetting(ctx, "retention.audit_days", 2)
	for _, ts := range []int64{now.Unix(), now.Add(-3 * 24 * time.Hour).Unix()} {
		_, err = db.InsertAudit(ctx, store.AuditEntry{TS: ts, Actor: "test", Action: "test", TargetType: "test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := Worker{DB: db}
	if err = w.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListAudit(ctx, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("retention %d %v", len(rows), err)
	}
	if w.lastDay.IsZero() || w.lastWeek.IsZero() {
		t.Fatal("maintenance not run")
	}
}
