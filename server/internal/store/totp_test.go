package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTOTPReplayIsAtomicAndDurable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "totp.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := db.CreateUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if db.ConsumeTOTP(ctx, u, now.Unix()/30, "123456", now) == nil {
				success.Add(1)
			}
		})
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("accepted %d duplicates", success.Load())
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.ConsumeTOTP(ctx, u, now.Unix()/30, "123456", now); err == nil {
		t.Fatal("replay after reopen accepted")
	}
	if err = db.ConsumeTOTP(ctx, u, now.Unix()/30+1, "123456", now.Add(30*time.Second)); err == nil {
		t.Fatal("same digits within90s accepted")
	}
}

func TestTOTPRejectsStaleCredentialsBeforeConsumingOrChangingState(t *testing.T) {
	for _, change := range []string{"password", "secret", "enabled"} {
		t.Run(change, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			u, err := db.CreateUser(ctx, "admin", "old-password")
			if err != nil {
				t.Fatal(err)
			}
			if err = db.SetTOTP(ctx, u.ID, "", "old-secret", true); err != nil {
				t.Fatal(err)
			}
			u, _ = db.GetUserByID(ctx, u.ID)
			switch change {
			case "password":
				err = db.UpdateUserPassword(ctx, u.ID, "new-password")
			case "secret":
				err = db.SetTOTP(ctx, u.ID, "old-secret", "new-secret", true)
			case "enabled":
				err = db.ResetTOTP(ctx, u.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			for _, mutation := range []*TOTPChange{nil, {Enabled: false, Secret: ""}} {
				err = db.ApplyTOTP(ctx, u, now.Unix()/30, "123456", now, mutation)
				if !errors.Is(err, ErrTOTPState) {
					t.Fatalf("stale %s accepted %v", change, err)
				}
			}
			var n int
			_ = db.QueryRowContext(ctx, "SELECT count(*) FROM totp_used").Scan(&n)
			if n != 0 {
				t.Fatal("rejected stale operation burned code")
			}
		})
	}
}
func TestTOTPEnableCASAndAuditRollback(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u, _ := db.CreateUser(ctx, "admin", "old-password")
	now := time.Now()
	_ = db.UpdateUserPassword(ctx, u.ID, "new-password")
	change := &TOTPChange{Secret: "encrypted", Enabled: true, Audit: AuditEntry{TS: now.Unix(), Actor: "admin", Action: "auth.totp_enable", TargetType: "user", TargetID: "1"}}
	if err := db.ApplyTOTP(ctx, u, now.Unix()/30, "123456", now, change); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("stale enrollment %v", err)
	}
	u, _ = db.GetUserByID(ctx, u.ID)
	_, err := db.Exec(`CREATE TRIGGER no_totp_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'test audit unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyTOTP(ctx, u, now.Unix()/30, "123456", now, change); err == nil {
		t.Fatal("audit error ignored")
	}
	fresh, _ := db.GetUserByID(ctx, u.ID)
	if fresh.TOTPEnabled || fresh.TOTPSecret != "" {
		t.Fatal("enable not rolled back")
	}
	var n int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM totp_used").Scan(&n)
	if n != 0 {
		t.Fatal("code burn not rolled back")
	}
}
