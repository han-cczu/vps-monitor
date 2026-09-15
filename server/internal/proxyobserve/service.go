package proxyobserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"
	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

var ErrInvalid = errors.New("invalid proxy observation")
var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)

type assembly struct {
	session            string
	seq, last          int64
	next, pages, bytes int
	complete           bool
	at                 int64
	started            time.Time
	items              map[string]proto.ObservedInstance
}
type Service struct {
	db      *store.DB
	mu      sync.Mutex
	pending map[int64]*assembly
	now     func() time.Time
}

func New(db *store.DB) *Service {
	return &Service{db: db, pending: map[int64]*assembly{}, now: time.Now}
}

func safe(s string, n int) bool {
	if len(s) > n || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func validInstance(i proto.ObservedInstance) bool {
	if !identifier.MatchString(i.ID) || (i.Core != "sing-box" && i.Core != "xray") || (i.Ownership != "external" && i.Ownership != "managed") || len(i.Inbounds) > 64 || len(i.Ports) > 512 || len(i.ConfigPaths) > 16 || len(i.Issues) > 64 {
		return false
	}
	for s, n := range map[string]int{i.Source: 48, i.Version: 64, i.Service: 128, i.Binary: 1024, i.Namespace: 128, i.ProcessStart: 32, i.StatsStatus: 48} {
		if !safe(s, n) {
			return false
		}
	}
	for _, p := range i.ConfigPaths {
		if !safe(p, 1024) {
			return false
		}
	}
	for _, s := range i.Issues {
		if !safe(s, 96) {
			return false
		}
	}
	for _, p := range i.Ports {
		if !safe(p.Address, 128) || p.Port < 1 || p.Port > 65535 || p.Network != "tcp" && p.Network != "udp" {
			return false
		}
	}
	for _, in := range i.Inbounds {
		if !identifier.MatchString(in.ID) || !safe(in.Tag, 128) || !safe(in.Protocol, 48) || !safe(in.Listen, 128) || !safe(in.Port, 128) || !safe(in.Transport, 48) || in.Users != nil && (*in.Users < 0 || *in.Users > 1000000) {
			return false
		}
		if u := in.Usage; u != nil {
			if u.Scope != "manager_total" && u.Scope != "reference" || u.Source != "x_ui_database" && u.Source != "sing-box_api" && u.Source != "xray_api" {
				return false
			}
			for _, v := range []*int64{u.Up, u.Down, u.Limit, u.ExpireAt} {
				if v != nil && *v < 0 {
					return false
				}
			}
		}
	}
	return true
}

// Receive rejects replay, out-of-order/incomplete pages, unknown fields and
// oversized inventories. A database transaction publishes only whole scans.
func (s *Service) Receive(ctx context.Context, id int64, session string, raw []byte) error {
	if len(raw) > proto.ProxyMaxFrame || session == "" {
		return ErrInvalid
	}
	var p proto.ProxyObservation
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p.Type != proto.TypeProxyObservation || p.Session != session || p.Sequence < 1 || p.Page < 0 || p.Pages < 1 || p.Pages > proto.ProxyMaxPages || p.Page >= p.Pages || len(p.Instances) > 1 {
		return ErrInvalid
	}
	for _, i := range p.Instances {
		if !validInstance(i) {
			return ErrInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.pending[id]
	if a == nil || a.session != session {
		a = &assembly{session: session}
		s.pending[id] = a
	}
	if p.Page == 0 {
		if p.Sequence <= a.last || p.Sequence <= a.seq {
			return ErrInvalid
		}
		a.seq = p.Sequence
		a.next = 0
		a.pages = p.Pages
		a.complete = p.ScanComplete
		a.at = p.CollectedAt
		a.started = s.now()
		a.items = map[string]proto.ObservedInstance{}
		a.bytes = 0
	}
	if p.Sequence != a.seq || p.Page != a.next || p.Pages != a.pages || p.ScanComplete != a.complete || p.CollectedAt != a.at || s.now().Sub(a.started) > 30*time.Second {
		return ErrInvalid
	}
	a.bytes += len(raw)
	if a.bytes > 4<<20 {
		return ErrInvalid
	}
	for _, item := range p.Instances {
		if old, ok := a.items[item.ID]; ok {
			before, after := old, item
			before.Inbounds = nil
			after.Inbounds = nil
			if !reflect.DeepEqual(before, after) {
				return ErrInvalid
			}
			item.Inbounds = append(old.Inbounds, item.Inbounds...)
		}
		if len(item.Inbounds) > proto.ProxyMaxInbounds {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, in := range item.Inbounds {
			if seen[in.ID] {
				return ErrInvalid
			}
			seen[in.ID] = true
		}
		a.items[item.ID] = item
	}
	if len(a.items) > proto.ProxyMaxInstances {
		return ErrInvalid
	}
	a.next++
	if a.next != a.pages {
		return nil
	}
	items := make([]proto.ObservedInstance, 0, len(a.items))
	count := 0
	for _, i := range a.items {
		items = append(items, i)
		count += len(i.Inbounds)
	}
	if count > 4096 {
		return ErrInvalid
	}
	if err := s.db.SaveProxyObservations(ctx, id, items, a.complete, s.now().Unix()); err != nil {
		return err
	}
	a.last = a.seq
	a.items = nil
	return nil
}
func (s *Service) Forget(id int64) { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }
func (s *Service) List(ctx context.Context, id int64, online bool) ([]store.ProxyObservationRow, error) {
	rows, err := s.db.ProxyObservations(ctx, id)
	for i := range rows {
		if !online || rows[i].Absent || s.now().Unix()-rows[i].ReceivedAt > 90 {
			rows[i].Stale = true
		}
	}
	return rows, err
}
