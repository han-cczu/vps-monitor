package alert

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vpsmon/server/internal/store"
)

// The first request completes a control action after Deliver has already read
// both candidates. The second request must never start using that stale list.
// The handler's database write also requires Send to run outside a write tx.
func TestDeliveryRechecksCandidatesAfterControlAction(t *testing.T) {
	for _, action := range []string{"resolve", "disable"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			db, id := alertDB(t)
			now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			var channel store.NotifyChannel
			var second store.AlertEvent
			var sends atomic.Int32
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if sends.Add(1) == 1 {
					controlCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
					defer cancel()
					var err error
					if action == "resolve" {
						err = db.ResolveAlert(controlCtx, second.ID, now.Unix(), true)
					} else {
						channel.Enabled = false
						err = db.SaveNotifyChannel(controlCtx, &channel)
					}
					if err != nil {
						t.Errorf("control action during Send: %v", err)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer receiver.Close()
			channel = webhook(t, db, receiver.URL)
			for _, key := range []string{"first", "second"} {
				e := store.AlertEvent{RuleKind: "server.offline", TargetType: "server", TargetID: id, Title: key, FiredAt: now.Unix(), DedupeKey: key}
				if created, err := db.FireAlert(ctx, &e, "", 0); err != nil || !created {
					t.Fatalf("fire %s: created=%v err=%v", key, created, err)
				}
				second = e
			}
			s := New(db, nil, nil, localSender(receiver))
			s.now = func() time.Time { return now }
			if err := s.Deliver(ctx, now); err != nil {
				t.Fatal(err)
			}
			if got := sends.Load(); got != 1 {
				t.Fatalf("sent %d requests; stale candidate was sent after %s completed", got, action)
			}
			stored, err := db.AlertEvent(ctx, second.ID)
			if err != nil || stored.NotifiedAt != nil {
				t.Fatalf("unclaimed event marked delivered: event=%+v err=%v", stored, err)
			}
		})
	}
}

func queuedDelivery(t *testing.T) (*store.DB, time.Time, store.AlertDelivery) {
	t.Helper()
	db, id := alertDB(t)
	webhook(t, db, "http://127.0.0.1/unused")
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	e := store.AlertEvent{RuleKind: "server.offline", TargetType: "server", TargetID: id, Title: "offline", FiredAt: now.Unix(), DedupeKey: "claim"}
	if created, err := db.FireAlert(context.Background(), &e, "", 0); err != nil || !created {
		t.Fatalf("fire: created=%v err=%v", created, err)
	}
	due, err := db.DueAlertDeliveries(context.Background(), now.Unix())
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%v err=%v", due, err)
	}
	return db, now, due[0]
}

func secondDeliveryDB(t *testing.T, db *store.DB) *store.DB {
	t.Helper()
	var seq int
	var name, path string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	return other
}

func TestDeliveryClaimSerializesConcurrentCandidates(t *testing.T) {
	ctx := context.Background()
	db, now, candidate := queuedDelivery(t)
	other := secondDeliveryDB(t, db)
	channel, err := db.NotifyChannel(ctx, candidate.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.Name = "edited after candidate snapshot"
	if err := db.SaveNotifyChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type result struct {
		claim *store.AlertDeliveryClaim
		err   error
	}
	results := make(chan result, 16)
	var wg sync.WaitGroup
	for i := range 16 {
		workerDB := []*store.DB{db, other}[i%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := workerDB.ClaimAlertDelivery(ctx, candidate, now.Unix())
			results <- result{claim, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	claims := 0
	for r := range results {
		if r.err != nil {
			t.Errorf("concurrent claim: %v", r.err)
		}
		if r.claim != nil {
			claims++
			if r.claim.Delivery.Attempts != 1 || r.claim.Channel.Name != channel.Name || r.claim.Event.ID != candidate.EventID {
				t.Errorf("claim did not contain fresh consistent data: %+v", r.claim)
			}
		}
	}
	if claims != 1 {
		t.Fatalf("same candidate claimed %d times across two database handles", claims)
	}
}

func TestDeliveryClaimCrashLeaseAndStaleFinish(t *testing.T) {
	ctx := context.Background()
	db, now, candidate := queuedDelivery(t)
	// A candidate without a persisted claim cannot acknowledge a send.
	if err := db.FinishAlertDelivery(ctx, candidate, now.Unix(), nil); err != nil {
		t.Fatal(err)
	}
	first, err := db.ClaimAlertDelivery(ctx, candidate, now.Unix())
	if err != nil || first == nil {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	// A replacement process sees the persisted lease, with no memory of the
	// first worker. It must wait until the lease expires before another send.
	restarted := secondDeliveryDB(t, db)
	retryAt := now.Unix() + store.AlertDeliveryLeaseSeconds
	if due, err := restarted.DueAlertDeliveries(ctx, retryAt-1); err != nil || len(due) != 0 {
		t.Fatalf("leased attempt was immediately due: %v err=%v", due, err)
	}
	due, err := restarted.DueAlertDeliveries(ctx, retryAt)
	if err != nil || len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("crashed attempt did not become due: %v err=%v", due, err)
	}
	second, err := restarted.ClaimAlertDelivery(ctx, due[0], retryAt)
	if err != nil || second == nil || second.Delivery.Attempts != 2 {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	if err := db.FinishAlertDelivery(ctx, first.Delivery, retryAt+1, nil); err != nil {
		t.Fatal(err)
	}
	event, err := db.AlertEvent(ctx, candidate.EventID)
	if err != nil || event.NotifiedAt != nil {
		t.Fatalf("stale finish acknowledged a newer claim: event=%+v err=%v", event, err)
	}
	if err := restarted.FinishAlertDelivery(ctx, second.Delivery, retryAt+2, nil); err != nil {
		t.Fatal(err)
	}
	event, err = db.AlertEvent(ctx, candidate.EventID)
	if err != nil || event.NotifiedAt == nil || *event.NotifiedAt != retryAt+2 {
		t.Fatalf("current claim could not finish: event=%+v err=%v", event, err)
	}
	if claim, err := db.ClaimAlertDelivery(ctx, second.Delivery, retryAt+100); err != nil || claim != nil {
		t.Fatalf("sent row was reclaimed: claim=%+v err=%v", claim, err)
	}
}

func TestDeliveryClaimRepeatedCrashesStillStopAfterThree(t *testing.T) {
	ctx := context.Background()
	db, now, candidate := queuedDelivery(t)
	for attempt := 1; attempt <= 3; attempt++ {
		claim, err := db.ClaimAlertDelivery(ctx, candidate, now.Unix())
		if err != nil || claim == nil || claim.Delivery.Attempts != attempt {
			t.Fatalf("attempt %d: claim=%+v err=%v", attempt, claim, err)
		}
		candidate = claim.Delivery
		now = now.Add(time.Duration(store.AlertDeliveryLeaseSeconds) * time.Second)
	}
	if due, err := db.DueAlertDeliveries(ctx, now.Unix()); err != nil || len(due) != 0 {
		t.Fatalf("fourth attempt offered: %v err=%v", due, err)
	}
	if claim, err := db.ClaimAlertDelivery(ctx, candidate, now.Unix()); err != nil || claim != nil {
		t.Fatalf("fourth attempt claimed: claim=%+v err=%v", claim, err)
	}
}

func TestDeliveryClaimAllowsRecordedOneShotReminder(t *testing.T) {
	ctx := context.Background()
	db, id := alertDB(t)
	var sends atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	webhook(t, db, receiver.URL)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	stamp := now.Unix()
	e := store.AlertEvent{RuleKind: "server.quota", TargetType: "server", TargetID: id, Title: "80%", FiredAt: stamp, ResolvedAt: &stamp, DedupeKey: "one-shot"}
	if created, err := db.FireAlert(ctx, &e, "period", 0); err != nil || !created {
		t.Fatalf("fire: created=%v err=%v", created, err)
	}
	s := New(db, nil, nil, localSender(receiver))
	s.now = func() time.Time { return now }
	if err := s.Deliver(ctx, now); err != nil {
		t.Fatal(err)
	}
	stored, err := db.AlertEvent(ctx, e.ID)
	if err != nil || stored.NotifiedAt == nil || sends.Load() != 1 {
		t.Fatalf("recorded reminder not delivered: event=%+v sends=%d err=%v", stored, sends.Load(), err)
	}
}
