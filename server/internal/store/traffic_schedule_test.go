package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestTrafficScheduleMigrationPreservesExistingPeriods(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "old-traffic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sub, _ := fs.Sub(migrationFS, "migrations")
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.DB, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers(id,name,token_hash,traffic_reset_day,created_at,updated_at) VALUES(1,'old','token',14,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO traffic_periods(server_id,period_start,period_end,in_bytes,out_bytes,calibration_revision) VALUES(1,100,200,30,40,2),(1,200,NULL,50,60,3)`); err != nil {
		t.Fatal(err)
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
	if err != nil || len(rows) != 2 || rows[0].Start != 200 || rows[0].Out != 60 || rows[0].CalibrationRevision != 3 || rows[0].NextReset != 0 || rows[1].Out != 40 || *rows[1].End != 200 {
		t.Fatalf("migration changed usage: %+v %v", rows, err)
	}
}
