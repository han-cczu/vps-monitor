package store

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestTrafficScheduleMigrationPreservesExistingPeriods(t *testing.T) {
	for _, version := range []int64{13, 15} {
		t.Run(fmt.Sprintf("from_version_%d", version), func(t *testing.T) {
			testTrafficScheduleMigration(t, version)
		})
	}
}

func testTrafficScheduleMigration(t *testing.T, version int64) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "old-traffic.db"))
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
	if _, err := provider.UpTo(ctx, version); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers(id,name,token_hash,traffic_reset_day,created_at,updated_at) VALUES(1,'old','token',14,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO traffic_periods(server_id,period_start,period_end,in_bytes,out_bytes,calibration_revision,calibrated_at) VALUES(1,100,200,30,40,2,150),(1,200,NULL,50,60,3,250)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO traffic_counters(server_id,last_rx,last_tx,last_ts) VALUES(1,500,600,300)`); err != nil {
		t.Fatal(err)
	}
	if version >= 15 {
		if _, err := db.Exec(`INSERT INTO proxy_observations(server_id,instance_id,snapshot_json,received_at) VALUES(1,'external','{"core":"xray"}',300)`); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := db.GetServer(ctx, 1)
	if err != nil || s.TrafficResetMode != "monthly" || s.TrafficResetDay != 14 {
		t.Fatalf("legacy schedule: %+v %v", s, err)
	}
	rows, err := db.TrafficHistory(ctx, 1, 12)
	if err != nil || len(rows) != 2 || rows[0].Start != 200 || rows[0].In != 50 || rows[0].Out != 60 || rows[0].CalibrationRevision != 3 || rows[0].CalibratedAt != 250 || rows[0].NextReset != 0 || rows[1].In != 30 || rows[1].Out != 40 || rows[1].End == nil || *rows[1].End != 200 {
		t.Fatalf("migration changed usage: %+v %v", rows, err)
	}
	var rx, tx, ts int64
	if err := db.QueryRow(`SELECT last_rx,last_tx,last_ts FROM traffic_counters WHERE server_id=1`).Scan(&rx, &tx, &ts); err != nil || rx != 500 || tx != 600 || ts != 300 {
		t.Fatalf("migration changed raw counters: %d %d %d %v", rx, tx, ts, err)
	}
	if version >= 15 {
		var snapshot string
		var received int64
		if err := db.QueryRow(`SELECT snapshot_json,received_at FROM proxy_observations WHERE server_id=1 AND instance_id='external'`).Scan(&snapshot, &received); err != nil || snapshot != `{"core":"xray"}` || received != 300 {
			t.Fatalf("migration changed external observation: %q %d %v", snapshot, received, err)
		}
	}
}
