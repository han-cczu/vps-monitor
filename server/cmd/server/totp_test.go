package main

import (
	"context"
	"path/filepath"
	"testing"
	"vpsmon/server/internal/store"
)

func TestResetTOTPCommandUsesIsolatedDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VM_DATA_DIR", dir)
	t.Setenv("VM_TZ", "UTC")
	db, err := store.Open(filepath.Join(dir, "vm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := db.CreateUser(ctx, "admin", "test hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetTOTP(ctx, u.ID, "", "encrypted fixture", true); err != nil {
		t.Fatal(err)
	}
	if err = resetTOTPCommand([]string{"admin"}); err != nil {
		t.Fatal(err)
	}
	u, err = db.GetUserByID(ctx, u.ID)
	if err != nil || u.TOTPEnabled || u.TOTPSecret != "" {
		t.Fatalf("reset %+v %v", u, err)
	}
	entries, err := db.ListAudit(ctx, 10, 0)
	if err != nil || len(entries) != 1 || entries[0].Action != "auth.totp_reset" {
		t.Fatalf("audit %+v %v", entries, err)
	}
}
