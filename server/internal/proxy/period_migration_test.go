package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"vpsmon/server/internal/store"
)

// Start with the actual historical schema and goose version table on disk,
// then reopen it through the current migration path. A fresh-schema-only test
// cannot detect columns accidentally added to an already-applied migration.
func TestSubscriberPeriodDateMigrationFromExistingVersions(t *testing.T) {
	for _, version := range []int64{7, 11} {
		t.Run(map[int64]string{7: "already_0007", 11: "already_0011"}[version], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "existing.db")
			db, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			history, err := goose.NewProvider(goose.DialectSQLite3, db.DB, os.DirFS("../store/migrations"))
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			if _, err := history.UpTo(ctx, version); err != nil {
				db.Close()
				t.Fatal(err)
			}
			var columns int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscribers') WHERE name='period_date'`).Scan(&columns); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if columns != 0 {
				db.Close()
				t.Fatal("historical migration already contains period_date; upgrade regression is not exercised")
			}
			loc := time.FixedZone("old-panel", 8*3600)
			start := time.Date(2026, 9, 1, 0, 0, 0, 0, loc).Unix()
			if _, err := db.Exec(`INSERT INTO subscribers(id,name,sub_token,uuid,password,ss_user_key,reset_day,period_start,traffic_used,created_at,updated_at) VALUES(1,'existing','existing-token','existing-uuid','password','ss-key',1,?,321,?,?)`, start, start, start); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			// Simulate replacing the binary while preserving the existing database.
			db, err = store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for range 2 {
				if err := db.Migrate(ctx); err != nil {
					t.Fatalf("upgrade existing v%d: %v", version, err)
				}
			}
			got, err := db.Proxy().Subscriber(ctx, 1)
			if err != nil || got.PeriodDate != "" {
				t.Fatalf("migration must leave timezone-aware backfill to startup: %+v %v", got, err)
			}
			e := NewEnforcer(db, nil, nil)
			e.now = func() time.Time { return time.Date(2026, 9, 14, 12, 0, 0, 0, loc) }
			for range 2 {
				if err := e.Initialize(ctx); err != nil {
					t.Fatalf("initialize upgraded v%d: %v", version, err)
				}
			}
			if err := e.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, err = db.Proxy().Subscriber(ctx, 1)
			if err != nil || got.PeriodDate != "2026-09-01" || got.PeriodStart != start || got.TrafficUsed != 321 || got.SubToken != "existing-token" {
				t.Fatalf("upgrade/backfill changed prior data: %+v %v", got, err)
			}
		})
	}
}

func TestSubscriberPeriodDateFreshDatabaseInitializes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.FixedZone("panel", 8*3600))
	e := NewEnforcer(db, nil, nil)
	e.now = func() time.Time { return now }
	if err := e.Initialize(ctx); err != nil {
		t.Fatalf("initialize empty database: %v", err)
	}
	s := New(db, nil)
	s.now = e.now
	sub, err := s.SaveSubscriber(ctx, 0, SubscriberInput{Name: ptr("new subscriber"), ResetDay: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.Proxy().Subscriber(ctx, sub.ID)
	if err != nil || got.PeriodDate != "2026-09-14" || got.TrafficUsed != 0 {
		t.Fatalf("new database initialization: %+v %v", got, err)
	}
}
