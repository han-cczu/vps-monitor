// Package proxy owns proxy data, rendering reconciliation and usage accounting.
// CRUD transactions announce committed changes through Notifier.
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"vpsmon/server/internal/audit"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/enforce"
	"vpsmon/server/internal/store"
)

type Notifier interface {
	NodeChanged(serverID int64, reason string)
}
type NoopNotifier struct{}

func (NoopNotifier) NodeChanged(int64, string) {}

type Service struct {
	AllowManage func(int64) bool
	db          *store.DB
	notifier    Notifier
	now         func() time.Time
	Events      func(hub.Event)
}

func (s *Service) guard(id int64) error {
	if s.AllowManage != nil && !s.AllowManage(id) {
		return ErrReadOnly
	}
	return nil
}

func New(db *store.DB, n Notifier) *Service {
	if n == nil {
		n = NoopNotifier{}
	}
	return &Service{db: db, notifier: n, now: clock.Now}
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func invalid(err error) error {
	if err == nil {
		return nil
	}
	return &ValidationError{Message: err.Error()}
}

var ErrConflict = errors.New("节点的端口和传输协议冲突")

func (s *Service) notify(ids []int64, reason string) {
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		s.notifier.NodeChanged(id, reason)
	}
}
func nodeIDs(sub *store.Subscriber) []int64 {
	ids := []int64{}
	for _, a := range sub.AssignedInbounds {
		ids = append(ids, a.ServerID)
	}
	return ids
}

// Audit payloads use an explicit allowlist. Advanced JSON may contain arbitrary
// credentials, so only its digest/size are recorded, never its full contents.
func auditValue(v any) any {
	switch x := v.(type) {
	case *store.Inbound:
		if x == nil {
			return nil
		}
		var settings map[string]any
		_ = json.Unmarshal(x.Settings, &settings)
		for _, field := range []string{"private_key", "server_psk", "obfs_password"} {
			if _, ok := settings[field]; ok {
				settings[field] = "***"
			}
		}
		return map[string]any{"id": x.ID, "server_id": x.ServerID, "tag": x.Tag, "protocol": x.Protocol, "listen_port": x.ListenPort, "settings": settings, "remark": x.Remark, "enabled": x.Enabled}
	case *store.Subscriber:
		if x == nil {
			return nil
		}
		copy := *x
		copy.SubToken = "***"
		copy.UUID = "***"
		copy.Password = "***"
		copy.SSUserKey = "***"
		return copy
	case *certs.Cert:
		if x == nil {
			return nil
		}
		return map[string]any{"server_id": x.ServerID, "sni": x.SNI, "fingerprint_sha256": x.FingerprintSHA256, "not_after": x.NotAfter, "key_pem": "***"}
	case *store.Advanced:
		if x == nil {
			return nil
		}
		sum := sha256.Sum256(x.ExtraJSON)
		return map[string]any{"server_id": x.ServerID, "sha256": hex.EncodeToString(sum[:]), "bytes": len(x.ExtraJSON), "extra_json": "***"}
	default:
		return v
	}
}
func (s *Service) record(ctx context.Context, q store.ProxyQueries, action, target string, id int64, before, after any) error {
	actor := audit.SystemActor
	if p, ok := auth.PrincipalFromContext(ctx); ok {
		actor = p.Name
	}
	encode := func(v any) string {
		v = auditValue(v)
		if v == nil {
			return ""
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	return q.Audit(ctx, store.AuditEntry{TS: s.now().Unix(), Actor: actor, Action: action, TargetType: target, TargetID: strconv.FormatInt(id, 10), Before: encode(before), After: encode(after), IP: audit.IPFromContext(ctx)})
}
func conflictError(err error) error {
	if err != nil && strings.Contains(err.Error(), "proxy_port_conflict") {
		return ErrConflict
	}
	return err
}

func (s *Service) Inbounds(ctx context.Context, serverID int64) ([]store.Inbound, error) {
	q := s.db.Proxy()
	if err := q.ServerExists(ctx, serverID); err != nil {
		return nil, err
	}
	return q.Inbounds(ctx, serverID)
}
func (s *Service) Inbound(ctx context.Context, id int64) (*store.Inbound, error) {
	return s.db.Proxy().Inbound(ctx, id)
}
func (s *Service) Subscribers(ctx context.Context) ([]*store.Subscriber, error) {
	users, err := s.db.Proxy().Subscribers(ctx)
	for _, user := range users {
		enforce.Decorate(user, s.now(), s.now().Location())
	}
	return users, err
}
func (s *Service) Subscriber(ctx context.Context, id int64) (*store.Subscriber, error) {
	user, err := s.db.Proxy().Subscriber(ctx, id)
	enforce.Decorate(user, s.now(), s.now().Location())
	return user, err
}

func (s *Service) publishPolicy(events []hub.Event, result *store.Subscriber) {
	if s.Events != nil {
		for _, event := range events {
			s.Events(event)
		}
	}
	now := s.now()
	enforce.Decorate(result, now, now.Location())
}
func (s *Service) Cert(ctx context.Context, serverID int64) (*certs.Cert, error) {
	q := s.db.Proxy()
	if err := q.ServerExists(ctx, serverID); err != nil {
		return nil, err
	}
	c, err := q.Cert(ctx, serverID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return c, err
}
func (s *Service) Core(ctx context.Context, serverID int64) (*store.NodeCore, error) {
	return s.db.Proxy().Core(ctx, serverID)
}
func (s *Service) Advanced(ctx context.Context, serverID int64) (*store.Advanced, error) {
	q := s.db.Proxy()
	if err := q.ServerExists(ctx, serverID); err != nil {
		return nil, err
	}
	return q.Advanced(ctx, serverID)
}

func checkText(s string, max int) error {
	if len([]rune(s)) > max || strings.ContainsRune(s, 0) {
		return fmt.Errorf("文本超出长度限制或包含非法字符")
	}
	return nil
}
