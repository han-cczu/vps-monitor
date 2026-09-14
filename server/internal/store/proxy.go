package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"vpsmon/server/internal/proxy/certs"
	"vpsmon/server/internal/proxy/model"
)

type Inbound = model.Inbound
type Subscriber struct {
	ID               int64        `json:"id"`
	Name             string       `json:"name"`
	Note             string       `json:"note"`
	Enabled          bool         `json:"enabled"`
	AutoDisabled     string       `json:"auto_disabled"`
	SubToken         string       `json:"sub_token,omitempty"`
	UUID             string       `json:"uuid,omitempty"`
	Password         string       `json:"password,omitempty"`
	SSUserKey        string       `json:"ss_user_key,omitempty"`
	TrafficLimit     int64        `json:"traffic_limit"`
	TrafficUsed      int64        `json:"traffic_used"`
	ResetDay         int          `json:"reset_day"`
	PeriodStart      int64        `json:"period_start"`
	ExpireAt         *string      `json:"expire_at"`
	CreatedAt        int64        `json:"created_at"`
	UpdatedAt        int64        `json:"updated_at"`
	AssignedInbounds []Assignment `json:"assigned_inbounds"`
	ServersCount     int          `json:"servers_count"`
}
type Assignment struct {
	InboundID  int64  `json:"inbound_id"`
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	Protocol   string `json:"protocol"`
	Port       int    `json:"port"`
}
type NodeCore struct {
	ServerID         int64    `json:"server_id"`
	Core             string   `json:"core"`
	DesiredVersion   *string  `json:"desired_version"`
	InstalledVersion *string  `json:"installed_version"`
	Running          bool     `json:"running"`
	AppliedRevision  int64    `json:"applied_revision"`
	DesiredRevision  int64    `json:"desired_revision"`
	ConfigSHA256     *string  `json:"config_sha256"`
	Listening        []string `json:"listening"`
	Firewall         *string  `json:"firewall"`
	LastError        *string  `json:"last_error"`
	UpdatedAt        *int64   `json:"updated_at"`
}
type Advanced struct {
	ServerID  int64           `json:"server_id"`
	ExtraJSON json.RawMessage `json:"extra_json"`
	UpdatedAt *int64          `json:"updated_at"`
}

// Queryer is implemented by DB, Tx and a connection in an immediate transaction.
type Queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type ProxyQueries struct{ DB Queryer }

func (db *DB) Proxy() ProxyQueries { return ProxyQueries{DB: db.DB} }

// WithProxyTx acquires SQLite's writer reservation before reading validation
// inputs. This prevents check-then-write races across service instances.
func (db *DB) WithProxyTx(ctx context.Context, fn func(ProxyQueries) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, "ROLLBACK")
	}()
	if err = fn(ProxyQueries{DB: conn}); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func (q ProxyQueries) ServerExists(ctx context.Context, id int64) error {
	var n int64
	return notFound(q.DB.QueryRowContext(ctx, "SELECT id FROM servers WHERE id=?", id).Scan(&n))
}
func (q ProxyQueries) Audit(ctx context.Context, e AuditEntry) error {
	_, err := q.DB.ExecContext(ctx, `INSERT INTO audit_log(ts,actor,action,target_type,target_id,"before","after",ip) VALUES(?,?,?,?,?,?,?,?)`, e.TS, e.Actor, e.Action, e.TargetType, e.TargetID, nullIfEmpty(e.Before), nullIfEmpty(e.After), nullIfEmpty(e.IP))
	return err
}

const inboundColumns = `id,server_id,tag,protocol,listen_port,settings,remark,enabled,created_at,updated_at`

func scanInbound(row scanner) (*Inbound, error) {
	var i Inbound
	var raw string
	err := row.Scan(&i.ID, &i.ServerID, &i.Tag, &i.Protocol, &i.ListenPort, &raw, &i.Remark, &i.Enabled, &i.CreatedAt, &i.UpdatedAt)
	i.Settings = json.RawMessage(raw)
	return &i, notFound(err)
}
func (q ProxyQueries) Inbound(ctx context.Context, id int64) (*Inbound, error) {
	return scanInbound(q.DB.QueryRowContext(ctx, `SELECT `+inboundColumns+` FROM inbounds WHERE id=?`, id))
}
func (q ProxyQueries) Inbounds(ctx context.Context, serverID int64) ([]Inbound, error) {
	rows, err := q.DB.QueryContext(ctx, `SELECT `+inboundColumns+` FROM inbounds WHERE server_id=? ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Inbound{}
	for rows.Next() {
		i, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}
func (q ProxyQueries) SaveInbound(ctx context.Context, i *Inbound) error {
	if i.ID == 0 {
		res, err := q.DB.ExecContext(ctx, `INSERT INTO inbounds(server_id,tag,protocol,listen_port,settings,remark,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, i.ServerID, i.Tag, i.Protocol, i.ListenPort, string(i.Settings), i.Remark, i.Enabled, i.CreatedAt, i.UpdatedAt)
		if err != nil {
			return err
		}
		i.ID, err = res.LastInsertId()
		return err
	}
	_, err := q.DB.ExecContext(ctx, `UPDATE inbounds SET tag=?,listen_port=?,settings=?,remark=?,enabled=?,updated_at=? WHERE id=?`, i.Tag, i.ListenPort, string(i.Settings), i.Remark, i.Enabled, i.UpdatedAt, i.ID)
	return err
}
func (q ProxyQueries) DeleteInbound(ctx context.Context, id int64) error {
	_, err := q.DB.ExecContext(ctx, "DELETE FROM inbounds WHERE id=?", id)
	return err
}
func (q ProxyQueries) Cert(ctx context.Context, id int64) (*certs.Cert, error) {
	var c certs.Cert
	err := q.DB.QueryRowContext(ctx, `SELECT server_id,sni,cert_pem,key_pem,fingerprint_sha256,not_after,created_at FROM certs WHERE server_id=?`, id).Scan(&c.ServerID, &c.SNI, &c.CertPEM, &c.KeyPEM, &c.FingerprintSHA256, &c.NotAfter, &c.CreatedAt)
	return &c, notFound(err)
}
func (q ProxyQueries) SaveCert(ctx context.Context, c *certs.Cert) error {
	_, err := q.DB.ExecContext(ctx, `INSERT INTO certs(server_id,sni,cert_pem,key_pem,fingerprint_sha256,not_after,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(server_id) DO UPDATE SET sni=excluded.sni,cert_pem=excluded.cert_pem,key_pem=excluded.key_pem,fingerprint_sha256=excluded.fingerprint_sha256,not_after=excluded.not_after,created_at=excluded.created_at`, c.ServerID, c.SNI, c.CertPEM, c.KeyPEM, c.FingerprintSHA256, c.NotAfter, c.CreatedAt)
	return err
}
func (q ProxyQueries) Advanced(ctx context.Context, id int64) (*Advanced, error) {
	a := &Advanced{ServerID: id, ExtraJSON: json.RawMessage(`{}`)}
	var raw string
	err := q.DB.QueryRowContext(ctx, `SELECT extra_json,updated_at FROM node_advanced WHERE server_id=?`, id).Scan(&raw, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	a.ExtraJSON = json.RawMessage(raw)
	return a, err
}
func (q ProxyQueries) SaveAdvanced(ctx context.Context, a *Advanced) error {
	_, err := q.DB.ExecContext(ctx, `INSERT INTO node_advanced(server_id,extra_json,updated_at) VALUES(?,?,?) ON CONFLICT(server_id) DO UPDATE SET extra_json=excluded.extra_json,updated_at=excluded.updated_at`, a.ServerID, string(a.ExtraJSON), a.UpdatedAt)
	return err
}
func (q ProxyQueries) Core(ctx context.Context, id int64) (*NodeCore, error) {
	c := &NodeCore{Listening: []string{}}
	var listening sql.NullString
	err := q.DB.QueryRowContext(ctx, `SELECT server_id,core,desired_version,installed_version,running,applied_revision,desired_revision,config_sha256,listening,firewall,last_error,updated_at FROM node_core WHERE server_id=?`, id).Scan(&c.ServerID, &c.Core, &c.DesiredVersion, &c.InstalledVersion, &c.Running, &c.AppliedRevision, &c.DesiredRevision, &c.ConfigSHA256, &listening, &c.Firewall, &c.LastError, &c.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if listening.Valid {
		if err = json.Unmarshal([]byte(listening.String), &c.Listening); err != nil {
			return nil, err
		}
	}
	return c, nil
}

const subscriberFields = `id,name,note,enabled,auto_disabled,traffic_limit,traffic_used,reset_day,period_start,expire_at,created_at,updated_at`

func scanSubscriber(row scanner, detail bool) (*Subscriber, error) {
	s := &Subscriber{AssignedInbounds: []Assignment{}}
	args := []any{&s.ID, &s.Name, &s.Note, &s.Enabled, &s.AutoDisabled, &s.TrafficLimit, &s.TrafficUsed, &s.ResetDay, &s.PeriodStart, &s.ExpireAt, &s.CreatedAt, &s.UpdatedAt}
	if detail {
		args = append(args, &s.SubToken, &s.UUID, &s.Password, &s.SSUserKey)
	}
	return s, notFound(row.Scan(args...))
}
func (q ProxyQueries) Subscriber(ctx context.Context, id int64) (*Subscriber, error) {
	s, err := scanSubscriber(q.DB.QueryRowContext(ctx, `SELECT `+subscriberFields+`,sub_token,uuid,password,ss_user_key FROM subscribers WHERE id=?`, id), true)
	if err != nil {
		return nil, err
	}
	err = q.assignments(ctx, []*Subscriber{s})
	return s, err
}
func (q ProxyQueries) Subscribers(ctx context.Context) ([]*Subscriber, error) {
	rows, err := q.DB.QueryContext(ctx, `SELECT `+subscriberFields+` FROM subscribers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := []*Subscriber{}
	for rows.Next() {
		s, err := scanSubscriber(rows, false)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	err = q.assignments(ctx, out)
	return out, err
}
func (q ProxyQueries) assignments(ctx context.Context, subscribers []*Subscriber) error {
	if len(subscribers) == 0 {
		return nil
	}
	byID := map[int64]*Subscriber{}
	servers := map[int64]map[int64]bool{}
	for _, s := range subscribers {
		byID[s.ID] = s
		servers[s.ID] = map[int64]bool{}
	}
	query := `SELECT a.subscriber_id,i.id,i.server_id,s.name,i.protocol,i.listen_port FROM subscriber_assignments a JOIN inbounds i ON i.id=a.inbound_id JOIN servers s ON s.id=i.server_id`
	args := []any{}
	if len(subscribers) == 1 {
		query += " WHERE a.subscriber_id=?"
		args = append(args, subscribers[0].ID)
	}
	query += " ORDER BY a.subscriber_id,i.server_id,i.id"
	rows, err := q.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var a Assignment
		if err = rows.Scan(&id, &a.InboundID, &a.ServerID, &a.ServerName, &a.Protocol, &a.Port); err != nil {
			return err
		}
		if s := byID[id]; s != nil {
			s.AssignedInbounds = append(s.AssignedInbounds, a)
			servers[id][a.ServerID] = true
		}
	}
	for id, s := range byID {
		s.ServersCount = len(servers[id])
	}
	return rows.Err()
}
func (q ProxyQueries) SaveSubscriber(ctx context.Context, s *Subscriber) error {
	args := []any{s.Name, s.Note, s.Enabled, s.AutoDisabled, s.SubToken, s.UUID, s.Password, s.SSUserKey, s.TrafficLimit, s.TrafficUsed, s.ResetDay, s.PeriodStart, s.ExpireAt, s.UpdatedAt}
	if s.ID == 0 {
		args = append(args, s.CreatedAt)
		res, err := q.DB.ExecContext(ctx, `INSERT INTO subscribers(name,note,enabled,auto_disabled,sub_token,uuid,password,ss_user_key,traffic_limit,traffic_used,reset_day,period_start,expire_at,updated_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
		if err != nil {
			return err
		}
		s.ID, err = res.LastInsertId()
		return err
	}
	args = append(args, s.ID)
	_, err := q.DB.ExecContext(ctx, `UPDATE subscribers SET name=?,note=?,enabled=?,auto_disabled=?,sub_token=?,uuid=?,password=?,ss_user_key=?,traffic_limit=?,traffic_used=?,reset_day=?,period_start=?,expire_at=?,updated_at=? WHERE id=?`, args...)
	return err
}
func (q ProxyQueries) DeleteSubscriber(ctx context.Context, id int64) error {
	_, err := q.DB.ExecContext(ctx, "DELETE FROM subscribers WHERE id=?", id)
	return err
}
func (q ProxyQueries) Assign(ctx context.Context, subscriberID int64, ids []int64) error {
	if _, err := q.DB.ExecContext(ctx, "DELETE FROM subscriber_assignments WHERE subscriber_id=?", subscriberID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := q.DB.ExecContext(ctx, "INSERT INTO subscriber_assignments(subscriber_id,inbound_id) VALUES(?,?)", subscriberID, id); err != nil {
			return err
		}
	}
	return nil
}
func (q ProxyQueries) ClearCurrentUsage(ctx context.Context, s *Subscriber) error {
	_, err := q.DB.ExecContext(ctx, "DELETE FROM subscriber_traffic WHERE subscriber_id=? AND period_start=?", s.ID, s.PeriodStart)
	return err
}
