package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// 升级前已有的 absent 记录：不能把 received_at 冒充成"确认消失的时刻"，
// 但升级后要立刻按新规则展示（进历史），并且仍然会过期。
func TestProxyObservationHistoryMigrationKeepsUnknownAbsentTime(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "observe-upgrade.db"))
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
	if _, err := provider.UpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO servers(id,name,token_hash,created_at,updated_at) VALUES(1,'legacy','token',1,1)`); err != nil {
		t.Fatal(err)
	}
	// 旧表没有 absent_at：一条仍然存在的记录，一条已经标记消失的记录。
	if _, err := db.ExecContext(ctx, `INSERT INTO proxy_observations(server_id,instance_id,snapshot_json,received_at,absent) VALUES(1,'live','{"id":"live","core":"xray"}',500,0),(1,'gone','{"id":"gone","core":"xray"}',400,1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	rows, scan, err := db.ProxyObservations(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if scan != nil {
		t.Fatal("scan state fabricated for a database that never had one", scan)
	}
	if len(rows) != 2 {
		t.Fatalf("existing observations lost: %+v", rows)
	}
	for _, row := range rows {
		if row.ID == "gone" {
			if !row.Absent {
				t.Fatal("legacy absent record no longer marked absent")
			}
			if row.AbsentAt != 0 {
				t.Fatalf("unknown absent time was fabricated: %d", row.AbsentAt)
			}
			if row.ReceivedAt != 400 {
				t.Fatalf("last observation time lost: %d", row.ReceivedAt)
			}
		}
		if row.ID == "live" && row.Absent {
			t.Fatal("existing instance marked absent by migration")
		}
	}

	// 未知时刻的历史记录仍然会过期：保留期退回按最后观测时间算，不会无限保留。
	at := int64(400 + 31*86400)
	if err := db.SaveProxyObservations(ctx, 1, nil, true, at, at); err != nil {
		t.Fatal(err)
	}
	rows, _, err = db.ProxyObservations(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "gone" {
			t.Fatal("unknown-time history never expires")
		}
	}

	// 升级后新标记的 absent 记录有准确时刻：确认消失的时间与最后观测时间分开记录，
	// 而且重复标记不会把它往后推。
	if err := db.SaveProxyObservations(ctx, 1, nil, true, at+100, at+100); err != nil {
		t.Fatal(err)
	}
	rows, _, err = db.ProxyObservations(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "live" && (row.AbsentAt != at || row.ReceivedAt != 500) {
			t.Fatalf("absent time and last observation time conflated: %+v", row)
		}
	}
}
