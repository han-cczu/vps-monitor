// Package traffic accounts node interface counters by calendar billing period.
package traffic

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

type Accountant struct {
	mu       sync.Mutex
	db       *store.DB
	bus      *hub.Bus
	now      func() time.Time
	servers  map[int64]store.Server
	counters map[int64]store.TrafficCounter
	periods  map[int64]store.TrafficPeriod
	closed   []store.TrafficPeriod
	dirty    bool
}

func New(db *store.DB, bus *hub.Bus) *Accountant {
	return &Accountant{db: db, bus: bus, now: clock.Now, servers: map[int64]store.Server{}, counters: map[int64]store.TrafficCounter{}, periods: map[int64]store.TrafficPeriod{}}
}

func (a *Accountant) Load(ctx context.Context) error {
	counters, periods, err := a.db.LoadTraffic(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	for _, c := range counters {
		a.counters[c.ServerID] = c
	}
	for _, p := range periods {
		a.periods[p.ServerID] = p
	}
	a.mu.Unlock()
	if err := a.Reload(ctx); err != nil {
		return err
	}
	return a.Rollover(ctx, a.now())
}

// Reload also makes newly-created nodes eligible before their first agent sample.
func (a *Accountant) Reload(ctx context.Context) error {
	servers, err := a.db.ListServers(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := map[int64]store.Server{}
	for _, s := range servers {
		next[s.ID] = s
		if _, ok := a.periods[s.ID]; !ok {
			a.periods[s.ID] = store.TrafficPeriod{ServerID: s.ID, Start: PeriodStart(a.now(), s.TrafficResetDay).Unix()}
			a.dirty = true
		}
	}
	for id := range a.servers {
		if _, ok := next[id]; !ok {
			delete(a.periods, id)
			delete(a.counters, id)
		}
	}
	a.servers = next
	return nil
}

func (a *Accountant) Forget(id int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.servers, id)
	delete(a.periods, id)
	delete(a.counters, id)
}

func (a *Accountant) OnMetrics(id int64, m *proto.Metrics) {
	if m == nil || m.Net.RxTotal < 0 || m.Net.TxTotal < 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.servers[id]
	if !ok {
		return
	}
	c, exists := a.counters[id]
	// A stale/duplicate sample must never look like a machine counter reset.
	if exists && m.TS <= c.LastTS {
		return
	}
	a.roll(id, a.now())
	a.counters[id] = store.TrafficCounter{ServerID: id, LastRX: m.Net.RxTotal, LastTX: m.Net.TxTotal, LastTS: m.TS}
	a.dirty = true
	if !exists {
		return
	}
	p := a.periods[id]
	previous := Used(p, s.TrafficMode)
	p.In = add(p.In, delta(c.LastRX, m.Net.RxTotal))
	p.Out = add(p.Out, delta(c.LastTX, m.Net.TxTotal))
	a.periods[id] = p
	used := Used(p, s.TrafficMode)
	if s.TrafficLimit > 0 && a.bus != nil {
		for _, threshold := range []int{80, 90, 100} {
			if float64(previous)/float64(s.TrafficLimit)*100 < float64(threshold) && float64(used)/float64(s.TrafficLimit)*100 >= float64(threshold) {
				a.bus.Publish(hub.Event{Kind: hub.EventServerTrafficThreshold, ServerID: id, TargetType: "server", TargetID: id, Threshold: threshold, At: a.now()})
			}
		}
	}
}

func delta(previous, current int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}
func add(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}
func Used(p store.TrafficPeriod, mode string) int64 {
	switch mode {
	case "in":
		return p.In
	case "out":
		return p.Out
	case "sum":
		return add(p.In, p.Out)
	default:
		return max(p.In, p.Out)
	}
}

// Boundary clamps reset day to the last day of each month, avoiding AddDate's
// January 31 -> March 3 normalization. All boundaries use the panel timezone.
func Boundary(year int, month time.Month, day int, loc *time.Location) time.Time {
	day = max(1, min(day, 31))
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	return time.Date(year, month, min(day, last), 0, 0, 0, 0, loc)
}
func PeriodStart(now time.Time, day int) time.Time {
	start := Boundary(now.Year(), now.Month(), day, now.Location())
	if start.After(now) {
		return Boundary(now.Year(), now.Month()-1, day, now.Location())
	}
	return start
}
func NextBoundary(start time.Time, day int) time.Time {
	b := Boundary(start.Year(), start.Month(), day, start.Location())
	if b.After(start) {
		return b
	}
	return Boundary(start.Year(), start.Month()+1, day, start.Location())
}
func (a *Accountant) roll(id int64, today time.Time) {
	p := a.periods[id]
	s := a.servers[id]
	for boundary := NextBoundary(time.Unix(p.Start, 0).In(today.Location()), s.TrafficResetDay); !boundary.After(today); boundary = NextBoundary(boundary, s.TrafficResetDay) {
		end := boundary.Unix()
		p.End = &end
		a.closed = append(a.closed, p)
		p = store.TrafficPeriod{ServerID: id, Start: end}
		a.dirty = true
	}
	a.periods[id] = p
}

func (a *Accountant) Rollover(ctx context.Context, today time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.servers {
		a.roll(id, today)
	}
	return a.flush(ctx, today.Format("2006-01-02"))
}
func (a *Accountant) Flush(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flush(ctx, "")
}
func (a *Accountant) flush(ctx context.Context, date string) error {
	if !a.dirty && date == "" {
		return nil
	}
	counters := make([]store.TrafficCounter, 0, len(a.counters))
	for _, c := range a.counters {
		counters = append(counters, c)
	}
	// Closed versions precede current versions to satisfy the unique-current index.
	periods := append([]store.TrafficPeriod{}, a.closed...)
	for _, p := range a.periods {
		periods = append(periods, p)
	}
	if err := a.db.SaveTraffic(ctx, counters, periods, date); err != nil {
		return err
	}
	a.closed = nil
	a.dirty = false
	return nil
}

func (a *Accountant) SnapshotFor(id int64) *hub.TrafficView {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.servers[id]
	if !ok {
		return nil
	}
	p := a.periods[id]
	return &hub.TrafficView{Used: Used(p, s.TrafficMode), Limit: s.TrafficLimit, Mode: s.TrafficMode, In: p.In, Out: p.Out, PeriodStart: p.Start, PeriodEndExpected: NextBoundary(time.Unix(p.Start, 0).In(clock.Location()), s.TrafficResetDay).Unix()}
}

func (a *Accountant) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Flush(flushCtx); err != nil {
			slog.Error("flush traffic on shutdown", "err", err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.Reload(ctx); err != nil {
				slog.Error("reload traffic servers", "err", err)
				continue
			}
			if err := a.Rollover(ctx, a.now()); err != nil {
				slog.Error("rollover/flush traffic", "err", err)
			}
		}
	}
}
