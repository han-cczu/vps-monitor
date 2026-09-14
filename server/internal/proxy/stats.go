package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/store"
)

const maxTraffic int64 = 9007199254740991
const maxTrafficBuckets = 65536

type usageKey struct {
	user, node, period, epoch int64
	date                      string
}
type usage struct{ up, down int64 }
type Stats struct {
	db       *store.DB
	mu       sync.Mutex
	flushMu  sync.Mutex
	pending  map[usageKey]usage
	inbounds map[int64]map[string]proto.Counter
	now      func() time.Time
	closed   bool
	// BeforeIngest is installed at startup. It advances a calendar boundary
	// before this sample captures the persistent period and manual-reset epoch.
	BeforeIngest func(context.Context, time.Time) error
}

func NewStats(db *store.DB) *Stats {
	return &Stats{db: db, pending: map[usageKey]usage{}, inbounds: map[int64]map[string]proto.Counter{}, now: clock.Now}
}
func addTraffic(a, b int64) int64 {
	if a >= maxTraffic-b {
		return maxTraffic
	}
	return a + b
}
func validateCounters(items []proto.Counter, maxItems int) error {
	if len(items) > maxItems {
		return fmt.Errorf("统计项目过多")
	}
	seen := map[string]bool{}
	for _, c := range items {
		if c.Name == "" || len(c.Name) > 128 || c.Up < 0 || c.Down < 0 || c.Up > 1<<50 || c.Down > 1<<50 || seen[c.Name] {
			return fmt.Errorf("统计计数非法或重复")
		}
		seen[c.Name] = true
	}
	return nil
}
func (s *Stats) Ingest(ctx context.Context, id int64, m proto.CoreStats) error {
	if m.Type != proto.TypeCoreStats {
		return fmt.Errorf("统计类型不合法")
	}
	if err := validateCounters(m.Users, 4096); err != nil {
		return err
	}
	if err := validateCounters(m.Inbounds, 1024); err != nil {
		return err
	}
	now := s.now()
	if s.BeforeIngest != nil {
		if err := s.BeforeIngest(ctx, now); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := store.ProxyQueries{DB: tx}
	if err = q.ServerExists(ctx, id); err != nil {
		return err
	}
	// Current assignments plus users still present in the last applied revision:
	// removing an assignment must not drop its final in-flight usage report.
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.period_start,s.usage_epoch FROM subscribers s WHERE
 EXISTS(SELECT 1 FROM subscriber_assignments a JOIN inbounds i ON i.id=a.inbound_id WHERE a.subscriber_id=s.id AND i.server_id=?) OR
 EXISTS(SELECT 1 FROM node_core n JOIN config_revisions r ON r.server_id=n.server_id AND r.revision=n.applied_revision,
 json_each(r.config_json,'$.experimental.v2ray_api.stats.users') u WHERE n.server_id=? AND u.value='sub-'||s.id)`, id, id)
	if err != nil {
		return err
	}
	allowed := map[int64]usageKey{}
	day := now.Format(time.DateOnly)
	for rows.Next() {
		k := usageKey{node: id, date: day}
		if err = rows.Scan(&k.user, &k.period, &k.epoch); err != nil {
			rows.Close()
			return err
		}
		allowed[k.user] = k
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	inbounds, err := q.Inbounds(ctx, id)
	if err != nil {
		return err
	}
	tags := map[string]bool{}
	for _, i := range inbounds {
		tags[i.Tag] = true
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	batch := map[usageKey]usage{}
	for _, c := range m.Users {
		num := strings.TrimPrefix(c.Name, "sub-")
		user, err := strconv.ParseInt(num, 10, 64)
		if err != nil || c.Name != "sub-"+strconv.FormatInt(user, 10) {
			continue
		}
		if key, ok := allowed[user]; ok && (c.Up != 0 || c.Down != 0) {
			batch[key] = usage{up: c.Up, down: c.Down}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return context.Canceled
	}
	added := 0
	for k := range batch {
		if _, ok := s.pending[k]; !ok {
			added++
		}
	}
	if len(s.pending)+added > maxTrafficBuckets {
		return fmt.Errorf("流量缓冲区达到上限，拒绝新批次")
	}
	for k, v := range batch {
		old := s.pending[k]
		s.pending[k] = usage{up: addTraffic(old.up, v.up), down: addTraffic(old.down, v.down)}
	}
	if s.inbounds[id] == nil {
		s.inbounds[id] = map[string]proto.Counter{}
	}
	for _, c := range m.Inbounds {
		if !tags[c.Name] {
			continue
		}
		old := s.inbounds[id][c.Name]
		old.Name = c.Name
		old.Up = addTraffic(old.Up, c.Up)
		old.Down = addTraffic(old.Down, c.Down)
		s.inbounds[id][c.Name] = old
	}
	for tag := range s.inbounds[id] {
		if !tags[tag] {
			delete(s.inbounds[id], tag)
		}
	}
	return nil
}
func (s *Stats) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	batch := s.pending
	s.pending = map[usageKey]usage{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := s.db.WithProxyTx(ctx, func(q store.ProxyQueries) error {
		mode := "sum"
		var raw string
		settingErr := q.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='enforce.count_mode'").Scan(&raw)
		if settingErr != nil && !errors.Is(settingErr, sql.ErrNoRows) {
			return settingErr
		}
		if settingErr == nil {
			if err := json.Unmarshal([]byte(raw), &mode); err != nil {
				return err
			}
			if mode != "sum" && mode != "download" {
				return fmt.Errorf("invalid enforce.count_mode")
			}
		}
		for k, v := range batch {
			// A reset or period change invalidates samples captured before it. Deleting
			// a node preserves already accepted usage; deleting the subscriber does not.
			var exists int
			if err := q.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM subscribers WHERE id=? AND usage_epoch=? AND period_start=?", k.user, k.epoch, k.period).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				continue
			}
			if _, err := q.DB.ExecContext(ctx, `INSERT INTO subscriber_traffic(subscriber_id,server_id,period_start,up_bytes,down_bytes) VALUES(?,?,?,?,?)
    ON CONFLICT(subscriber_id,server_id,period_start) DO UPDATE SET up_bytes=MIN(?,up_bytes+excluded.up_bytes),down_bytes=MIN(?,down_bytes+excluded.down_bytes)`, k.user, k.node, k.period, v.up, v.down, maxTraffic, maxTraffic); err != nil {
				return err
			}
			if _, err := q.DB.ExecContext(ctx, `INSERT INTO subscriber_traffic_daily(subscriber_id,date,up_bytes,down_bytes) VALUES(?,?,?,?)
    ON CONFLICT(subscriber_id,date) DO UPDATE SET up_bytes=MIN(?,up_bytes+excluded.up_bytes),down_bytes=MIN(?,down_bytes+excluded.down_bytes)`, k.user, k.date, v.up, v.down, maxTraffic, maxTraffic); err != nil {
				return err
			}
			// Charge only this committed delta. Re-aggregating historical raw counters
			// would retroactively change bills when count_mode is edited.
			counted := v.down
			if mode == "sum" {
				counted = addTraffic(counted, v.up)
			}
			if _, err := q.DB.ExecContext(ctx, "UPDATE subscribers SET traffic_used=MIN(?,traffic_used+?) WHERE id=?", maxTraffic, counted, k.user); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for k, v := range batch {
			old := s.pending[k]
			s.pending[k] = usage{up: addTraffic(old.up, v.up), down: addTraffic(old.down, v.down)}
		}
	}
	return err
}
func (s *Stats) Inbounds(id int64) []proto.Counter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []proto.Counter{}
	for _, v := range s.inbounds[id] {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b proto.Counter) int { return strings.Compare(a.Name, b.Name) })
	return out
}
func (s *Stats) Forget(id int64) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.inbounds, id) }

func (s *Stats) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.Flush(ctx)
}
