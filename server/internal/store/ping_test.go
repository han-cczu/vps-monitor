package store

import (
	"context"
	"strconv"
	"testing"
)

func TestPingHistoryBucketsAndLoss(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	server, err := db.CreateServer(ctx, ServerInput{Name: "ping", Currency: "CNY", BillingCycle: "month", TrafficResetDay: 1, TrafficMode: "sum"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	a, b := 10.0, 30.0
	for _, bucket := range []int64{60, 600, 3600} {
		t.Run(strconv.FormatInt(bucket, 10), func(t *testing.T) {
			db.ExecContext(ctx, "DELETE FROM ping_results")
			base := bucket * 100
			rows := []PingResult{{server.ID, 1, base, &a}, {server.ID, 1, base + 1, &b}, {server.ID, 1, base + 2, nil}, {server.ID, 1, base + bucket, nil}, {server.ID, 1, base + 2*bucket, &b}}
			if err := db.InsertPingResults(ctx, rows); err != nil {
				t.Fatal(err)
			}
			got, err := db.PingHistory(ctx, server.ID, 1, base, base+2*bucket, bucket)
			if err != nil || len(got) != 2 {
				t.Fatalf("got=%v err=%v", got, err)
			}
			if *got[0].Avg != 20 || *got[0].Max != 30 || got[0].Samples != 3 || got[0].LossPct < 33.33 || got[0].LossPct > 33.34 {
				t.Fatalf("first=%+v", got[0])
			}
			if got[1].Avg != nil || got[1].Max != nil || got[1].LossPct != 100 {
				t.Fatalf("lost=%+v", got[1])
			}
		})
	}
}
func TestPingScopeNullAndEmptyRemainDistinct(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, ids := range [][]int64{nil, {}, {7, 9}} {
		task, err := db.CreatePingTask(ctx, PingTaskInput{Name: "scope", Target: "localhost", Kind: "icmp", IntervalSec: 60, Enabled: true, ServerIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		if (task.ServerIDs == nil) != (ids == nil) || task.AppliesTo(7) != (ids == nil || len(ids) > 0) {
			t.Fatalf("scope=%+v", task)
		}
	}
}

func TestPingTaskIDsAreNotReused(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	in := PingTaskInput{Name: "task", Target: "localhost", Kind: "icmp", IntervalSec: 60, Enabled: true}
	old, err := db.CreatePingTask(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.DeletePingTask(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	next, err := db.CreatePingTask(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID <= old.ID {
		t.Fatal("deleted task ID reused; in-flight reports could attach to new task")
	}
}
