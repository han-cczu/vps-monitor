package store

import (
	"context"
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
			if db.ConsumeTOTP(ctx, u.ID, now.Unix()/30, "123456", now) == nil {
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
	if err = db.ConsumeTOTP(ctx, u.ID, now.Unix()/30, "123456", now); err == nil {
		t.Fatal("replay after reopen accepted")
	}
	if err = db.ConsumeTOTP(ctx, u.ID, now.Unix()/30+1, "123456", now.Add(30*time.Second)); err == nil {
		t.Fatal("same digits within90s accepted")
	}
}
