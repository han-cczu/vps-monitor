package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

type minute struct {
	TS             int64
	CPU, Mem, Disk float64
	Count          int
}
type Service struct {
	db       *store.DB
	registry *hub.Registry
	events   <-chan hub.Event
	sender   *Sender
	now      func() time.Time
	started  time.Time
	mu       sync.Mutex
	windows  map[int64]*[16]minute
	coreDown map[int64]time.Time
}

// Subscribe synchronously before starting billing/policy publishers.
func New(db *store.DB, registry *hub.Registry, bus *hub.Bus, sender *Sender) *Service {
	s := &Service{db: db, registry: registry, sender: sender, now: clock.Now, started: clock.Now(), windows: map[int64]*[16]minute{}, coreDown: map[int64]time.Time{}}
	if bus != nil {
		s.events = bus.Subscribe(256)
	}
	return s
}
func (s *Service) OnMetrics(id int64, m *proto.Metrics) {
	if m == nil || s.registry == nil {
		return
	}
	st, ok := s.registry.Get(id)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ring := s.windows[id]
	if ring == nil {
		ring = &[16]minute{}
		s.windows[id] = ring
	}
	ts := s.now().Unix() / 60
	idx := ts % 16
	if ring[idx].TS != ts {
		ring[idx] = minute{TS: ts}
	}
	p := &ring[idx]
	p.CPU += m.CPU
	if st.Host.MemTotal > 0 {
		p.Mem += float64(m.MemUsed) / float64(st.Host.MemTotal) * 100
	}
	if st.Host.DiskTotal > 0 {
		p.Disk = float64(m.DiskUsed) / float64(st.Host.DiskTotal) * 100
	}
	p.Count++
}

// Sustained needs every completed minute; missing minutes and partial current
// minutes cannot fabricate ten minutes of sustained pressure.
func sustained(ring *[16]minute, now time.Time, kind string, p Params) bool {
	if ring == nil {
		return false
	}
	last := now.Unix()/60 - 1
	for i := 0; i < p.Minutes; i++ {
		ts := last - int64(i)
		m := ring[ts%16]
		if m.TS != ts || m.Count == 0 {
			return false
		}
		v := m.CPU / float64(m.Count)
		if kind == "server.mem" {
			v = m.Mem / float64(m.Count)
		} else if kind == "server.disk" {
			v = m.Disk
		}
		if v <= p.Percent {
			return false
		}
	}
	return true
}
func (s *Service) rules(ctx context.Context) (map[string]store.AlertRule, error) {
	rows, err := s.db.AlertRules(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]store.AlertRule{}
	for _, r := range rows {
		out[r.Kind] = r
	}
	return out, nil
}
func (s *Service) cooldown(ctx context.Context) int64 {
	minutes := 30
	if _, err := s.db.GetSetting(ctx, "alert.cooldown_minutes", &minutes); err != nil {
		minutes = 30
	}
	return int64(max(0, min(10080, minutes))) * 60
}
func (s *Service) fire(ctx context.Context, kind, target string, id int64, suffix, occurrence, title, message, level string, now time.Time, oneShot bool) error {
	e := store.AlertEvent{RuleKind: kind, TargetType: target, TargetID: id, Level: level, Title: title, Message: message, FiredAt: now.Unix(), DedupeKey: kind + ":" + target + ":" + strconv.FormatInt(id, 10) + suffix}
	if oneShot {
		t := now.Unix()
		e.ResolvedAt = &t
	}
	_, err := s.db.FireAlert(ctx, &e, occurrence, s.cooldown(ctx))
	return err
}

func (s *Service) Evaluate(ctx context.Context, now time.Time) error {
	rules, err := s.rules(ctx)
	if err != nil {
		return err
	}
	if s.registry == nil {
		return nil
	}
	views := s.registry.SnapshotViews(ctx)
	active := map[string]bool{}
	unknown := map[string]bool{}
	validIDs := map[int64]bool{}
	for _, v := range views {
		validIDs[v.ID] = true
		for _, kind := range []string{"server.offline", "server.cpu", "server.mem", "server.disk", "core.down"} {
			r := rules[kind]
			if !r.Enabled {
				continue
			}
			p, err := ParseParams(r)
			if err != nil {
				continue
			}
			trigger := false
			message := ""
			key := kind + ":server:" + strconv.FormatInt(v.ID, 10)
			switch kind {
			case "server.offline":
				last := s.started
				if v.LastSeen != nil {
					last = time.Unix(*v.LastSeen, 0)
				}
				trigger = !v.Online && now.Sub(last) >= time.Duration(p.Minutes)*time.Minute
				message = fmt.Sprintf("节点：%s\n超过 %d 分钟未收到指标。", v.Name, p.Minutes)
			case "core.down":
				if !v.Online || v.Core == nil {
					unknown[key] = true
					continue
				}
				var core struct{ Installed, Running bool }
				raw, _ := json.Marshal(v.Core)
				_ = json.Unmarshal(raw, &core)
				s.mu.Lock()
				if v.Online && core.Installed && !core.Running {
					if s.coreDown[v.ID].IsZero() {
						s.coreDown[v.ID] = now
					}
					trigger = now.Sub(s.coreDown[v.ID]) >= time.Duration(p.Minutes)*time.Minute
				} else {
					delete(s.coreDown, v.ID)
				}
				s.mu.Unlock()
				message = fmt.Sprintf("节点：%s\n已安装的 sing-box 超过 %d 分钟未运行。", v.Name, p.Minutes)
			default:
				s.mu.Lock()
				unknown[key] = !v.Online || !complete(s.windows[v.ID], now, p.Minutes) || (kind == "server.mem" && v.Mem.Total <= 0) || (kind == "server.disk" && v.Disk.Total <= 0)
				trigger = !unknown[key] && sustained(s.windows[v.ID], now, kind, p)
				s.mu.Unlock()
				message = fmt.Sprintf("节点：%s\n连续 %d 个完整分钟的平均值超过 %.1f%%。", v.Name, p.Minutes, p.Percent)
				if kind == "server.disk" {
					message = fmt.Sprintf("节点：%s\n连续 %d 个完整分钟的末次磁盘水位超过 %.1f%%。", v.Name, p.Minutes, p.Percent)
				}
			}
			if trigger {
				key := kind + ":server:" + strconv.FormatInt(v.ID, 10)
				active[key] = true
				if err := s.fire(ctx, kind, "server", v.ID, "", "", Kinds[kind], message, "warning", now, false); err != nil {
					return err
				}
			}
		}
		if r := rules["ping.loss"]; r.Enabled {
			p, err := ParseParams(r)
			if err == nil {
				for _, ping := range v.Ping {
					key := "ping.loss:server:" + strconv.FormatInt(v.ID, 10) + ":" + strconv.FormatInt(ping.TaskID, 10)
					if !v.Online || ping.LastTS == nil || now.Unix()-*ping.LastTS > 300 {
						unknown[key] = true
						continue
					}
					if v.Online && ping.LastTS != nil && now.Unix()-*ping.LastTS <= 300 && ping.Loss > p.Percent {
						suffix := ":" + strconv.FormatInt(ping.TaskID, 10)
						key := "ping.loss:server:" + strconv.FormatInt(v.ID, 10) + suffix
						active[key] = true
						if err := s.fire(ctx, "ping.loss", "server", v.ID, suffix, "", Kinds["ping.loss"], fmt.Sprintf("节点：%s\n任务：%s\n最近 30 次窗口丢包 %.1f%%（阈值 %.1f%%）。", v.Name, ping.Name, ping.Loss, p.Percent), "warning", now, false); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	open, err := s.db.OpenAlerts(ctx)
	if err != nil {
		return err
	}
	for _, e := range open {
		switch e.RuleKind {
		case "server.offline", "server.cpu", "server.mem", "server.disk", "core.down", "ping.loss":
			if !active[e.DedupeKey] && !unknown[e.DedupeKey] {
				if err := s.db.ResolveAlert(ctx, e.ID, now.Unix(), validIDs[e.TargetID] && rules[e.RuleKind].Enabled); err != nil {
					return err
				}
			}
		}
	}
	s.mu.Lock()
	for id := range s.windows {
		if !validIDs[id] {
			delete(s.windows, id)
			delete(s.coreDown, id)
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) Handle(ctx context.Context, event hub.Event) error {
	kind := string(event.Kind)
	now := event.At
	if now.IsZero() {
		now = s.now()
	}
	if kind == "subscriber.restored" {
		open, err := s.db.OpenAlerts(ctx)
		if err != nil {
			return err
		}
		for _, e := range open {
			if e.TargetType == "subscriber" && e.TargetID == event.TargetID {
				if err := s.db.ResolveAlert(ctx, e.ID, now.Unix(), false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	switch kind {
	case "server.traffic", "server.expire", "subscriber.quota", "subscriber.expired", "core.apply_failed":
	default:
		return nil
	}
	rules, err := s.rules(ctx)
	if err != nil {
		return err
	}
	r := rules[kind]
	if !r.Enabled {
		return nil
	}
	p, err := ParseParams(r)
	if err != nil {
		return err
	}
	target, id := event.TargetType, event.TargetID
	if target == "" {
		target = "server"
	}
	if id == 0 {
		id = event.ServerID
	}
	if id <= 0 {
		return nil
	}
	if (kind == "subscriber.quota" || kind == "subscriber.expired") && target != "subscriber" {
		return nil
	}
	if kind != "subscriber.quota" && kind != "subscriber.expired" && target != "server" {
		return nil
	}
	suffix, occurrence := "", ""
	message := event.Message
	level := "warning"
	name := fmt.Sprintf("%s #%d", target, id)
	if target == "server" {
		node, err := s.db.GetServer(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		name = node.Name
	} else if target == "subscriber" {
		err := s.db.QueryRowContext(ctx, `SELECT name FROM subscribers WHERE id=?`, id).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	switch kind {
	case "server.traffic", "subscriber.quota":
		found := false
		for _, v := range p.Percents {
			if v == event.Threshold {
				found = true
			}
		}
		if !found {
			return nil
		}
		suffix = ":" + strconv.Itoa(event.Threshold)
		if kind == "server.traffic" {
			var start int64
			_ = s.db.QueryRowContext(ctx, `SELECT period_start FROM traffic_periods WHERE server_id=? AND period_end IS NULL`, id).Scan(&start)
			occurrence = strconv.FormatInt(start, 10)
			if s.registry != nil {
				for _, v := range s.registry.SnapshotViews(ctx) {
					if v.ID == id && v.Traffic != nil {
						occurrence = strconv.FormatInt(v.Traffic.PeriodStart, 10)
					}
				}
			}
		} else {
			_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(period_start,'') FROM subscribers WHERE id=?`, id).Scan(&occurrence)
		}
		if occurrence == "" || occurrence == "0" {
			occurrence = now.Format("2006-01")
		}
		message = fmt.Sprintf("用量已达到套餐的 %d%%。", event.Threshold)
		if event.Threshold >= 100 {
			level = "critical"
		}
	case "server.expire":
		found := false
		for _, v := range p.Days {
			if v == event.Threshold {
				found = true
			}
		}
		if !found {
			return nil
		}
		suffix = ":" + strconv.Itoa(event.Threshold)
		occurrence = now.Format("2006-01-02")
		message = fmt.Sprintf("距离到期还有 %d 天。", event.Threshold)
	case "subscriber.expired":
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(expire_at,'') FROM subscribers WHERE id=?`, id).Scan(&occurrence)
		if occurrence == "" {
			occurrence = now.Format("2006-01-02")
		}
		message = "订阅已到期并自动停用。"
		level = "critical"
	case "core.apply_failed":
		message = "sing-box 配置应用失败，请检查节点代理页的错误与修订记录。"
		level = "critical"
	}
	return s.fire(ctx, kind, target, id, suffix, occurrence, Kinds[kind], fmt.Sprintf("%s：%s\n%s", map[string]string{"server": "节点", "subscriber": "订阅用户"}[target], name, message), level, now, true)
}

func (s *Service) Deliver(ctx context.Context, now time.Time) error {
	due, err := s.db.DueAlertDeliveries(ctx, now.Unix())
	if err != nil {
		return err
	}
	for _, d := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		claim, err := s.db.ClaimAlertDelivery(ctx, d, s.now().Unix())
		if err != nil {
			return err
		}
		if claim == nil {
			continue
		}
		// The persisted claim is the send-attempt boundary. Control actions
		// committed before it prevent sending; an already started attempt may
		// finish after a later cancellation. Never hold a DB tx during HTTP.
		// Bound the request below the 30s claim lease even for injected clients.
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = s.sender.Send(sendCtx, claim.Channel, claim.Event, d.Recovery)
		cancel()
		if finishErr := s.db.FinishAlertDelivery(ctx, claim.Delivery, s.now().Unix(), err); finishErr != nil {
			return finishErr
		}
		if err != nil {
			slog.Warn("alert delivery failed", "event_id", d.EventID, "channel_id", d.ChannelID, "attempt", claim.Delivery.Attempts, "reason", err.Error())
		}
	}
	return nil
}
func (s *Service) Test(ctx context.Context, id int64) error {
	channel, err := s.db.NotifyChannel(ctx, id)
	if err != nil {
		return err
	}
	return s.sender.Send(ctx, *channel, store.AlertEvent{RuleKind: "test", Level: "info", Title: "VPS Monitor 测试通知", Message: "通知渠道连接测试。", FiredAt: s.now().Unix()}, false)
}
func (s *Service) Run(ctx context.Context) {
	if err := s.Warm(ctx); err != nil {
		slog.Error("warm alert windows", "err", err)
	}
	deliverDone := make(chan struct{})
	go func() {
		defer close(deliverDone)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := s.Deliver(ctx, s.now()); err != nil && ctx.Err() == nil {
					slog.Error("deliver alerts", "err", err)
				}
			}
		}
	}()
	defer func() { <-deliverDone }()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	if err := s.Evaluate(ctx, s.now()); err != nil {
		slog.Error("evaluate alerts", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-s.events:
			if err := s.Handle(ctx, e); err != nil {
				slog.Error("handle alert event", "kind", e.Kind, "err", err)
			}
		case <-ticker.C:
			if err := s.Evaluate(ctx, s.now()); err != nil {
				slog.Error("evaluate alerts", "err", err)
			}
		}
	}
}

func complete(ring *[16]minute, now time.Time, count int) bool {
	if ring == nil {
		return false
	}
	for i := 1; i <= count; i++ {
		ts := now.Unix()/60 - int64(i)
		if ring[ts%16].TS != ts || ring[ts%16].Count == 0 {
			return false
		}
	}
	return true
}

// Warm only runs during startup; the polling evaluator reads its bounded ring.
func (s *Service) Warm(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT m.server_id,m.ts,m.cpu_avg,
 CASE WHEN h.mem_total>0 THEN 100.0*m.mem_used/h.mem_total ELSE 0 END,
 CASE WHEN h.disk_total>0 THEN 100.0*m.disk_used/h.disk_total ELSE 0 END
 FROM metrics_minute m LEFT JOIN server_host_info h ON h.server_id=m.server_id WHERE m.ts>=? AND m.ts<?`, s.now().Add(-16*time.Minute).Unix(), s.now().Truncate(time.Minute).Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var id, ts int64
		var m minute
		if err := rows.Scan(&id, &ts, &m.CPU, &m.Mem, &m.Disk); err != nil {
			return err
		}
		m.TS = ts / 60
		m.Count = 1
		ring := s.windows[id]
		if ring == nil {
			ring = &[16]minute{}
			s.windows[id] = ring
		}
		if ring[m.TS%16].TS <= m.TS {
			ring[m.TS%16] = m
		}
	}
	return rows.Err()
}
