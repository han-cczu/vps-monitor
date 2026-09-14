package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"vpsmon/proto"
	"vpsmon/server/internal/proxy/certs"
)

type RenderData struct {
	Server         Server
	Inbounds       []Inbound
	UsersByInbound map[int64][]Subscriber
	Cert           *certs.Cert
	Extra          json.RawMessage
	Version        string
}

// RenderData must be read within one transaction for a consistent input snapshot.
func (q ProxyQueries) RenderData(ctx context.Context, id int64) (RenderData, error) {
	d := RenderData{Server: Server{ID: id}, UsersByInbound: map[int64][]Subscriber{}}
	if err := q.ServerExists(ctx, id); err != nil {
		return d, err
	}
	var err error
	d.Inbounds, err = q.Inbounds(ctx, id)
	if err != nil {
		return d, err
	}
	d.Cert, err = q.Cert(ctx, id)
	if errors.Is(err, ErrNotFound) {
		d.Cert = nil
	} else if err != nil {
		return d, err
	}
	advanced, err := q.Advanced(ctx, id)
	if err != nil {
		return d, err
	}
	d.Extra = advanced.ExtraJSON
	var version string
	err = q.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='core.current_version'").Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	if version != "" {
		if err = json.Unmarshal([]byte(version), &d.Version); err != nil {
			return d, err
		}
	}
	rows, err := q.DB.QueryContext(ctx, `SELECT a.inbound_id,s.id,s.enabled,s.auto_disabled,s.uuid,s.password,s.ss_user_key
 FROM subscriber_assignments a JOIN inbounds i ON i.id=a.inbound_id JOIN subscribers s ON s.id=a.subscriber_id
 WHERE i.server_id=? ORDER BY a.inbound_id,s.id`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var inbound int64
		var s Subscriber
		if err = rows.Scan(&inbound, &s.ID, &s.Enabled, &s.AutoDisabled, &s.UUID, &s.Password, &s.SSUserKey); err != nil {
			return d, err
		}
		d.UsersByInbound[inbound] = append(d.UsersByInbound[inbound], s)
	}
	return d, rows.Err()
}

type Revision struct {
	ServerID   int64           `json:"server_id"`
	Revision   int64           `json:"revision"`
	ConfigJSON json.RawMessage `json:"config_json,omitempty"`
	SHA256     string          `json:"sha256"`
	CreatedAt  int64           `json:"created_at"`
	CreatedBy  string          `json:"created_by"`
	Version    string          `json:"version"`
	Ports      []string        `json:"ports"`
	Applied    bool            `json:"applied"`
}

const revisionFields = `server_id,revision,config_json,sha256,created_at,created_by,version,ports`

func scanRevision(row scanner) (*Revision, error) {
	var r Revision
	var config, ports string
	err := row.Scan(&r.ServerID, &r.Revision, &config, &r.SHA256, &r.CreatedAt, &r.CreatedBy, &r.Version, &ports)
	if err != nil {
		return nil, notFound(err)
	}
	r.ConfigJSON = json.RawMessage(config)
	if err = json.Unmarshal([]byte(ports), &r.Ports); err != nil {
		return nil, err
	}
	return &r, nil
}
func (q ProxyQueries) Revision(ctx context.Context, id, revision int64) (*Revision, error) {
	if revision == 0 {
		return scanRevision(q.DB.QueryRowContext(ctx, "SELECT "+revisionFields+" FROM config_revisions WHERE server_id=? ORDER BY revision DESC LIMIT 1", id))
	}
	return scanRevision(q.DB.QueryRowContext(ctx, "SELECT "+revisionFields+" FROM config_revisions WHERE server_id=? AND revision=?", id, revision))
}
func (q ProxyQueries) Revisions(ctx context.Context, id int64) ([]Revision, error) {
	core, err := q.Core(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := q.DB.QueryContext(ctx, "SELECT "+revisionFields+" FROM config_revisions WHERE server_id=? ORDER BY revision DESC LIMIT 20", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		r.ConfigJSON = nil
		r.Applied = r.Revision == core.AppliedRevision && core.ConfigSHA256 != nil && *core.ConfigSHA256 == r.SHA256
		out = append(out, *r)
	}
	return out, rows.Err()
}
func (q ProxyQueries) InputHash(ctx context.Context, id int64) (string, error) {
	var hash string
	err := q.DB.QueryRowContext(ctx, "SELECT input_sha256 FROM node_core WHERE server_id=?", id).Scan(&hash)
	return hash, notFound(err)
}
func (q ProxyQueries) SaveRevision(ctx context.Context, r *Revision, inputHash string) error {
	ports, _ := json.Marshal(r.Ports)
	if _, err := q.DB.ExecContext(ctx, `INSERT INTO config_revisions(server_id,revision,config_json,sha256,created_at,created_by,version,ports) VALUES(?,?,?,?,?,?,?,?)`, r.ServerID, r.Revision, string(r.ConfigJSON), r.SHA256, r.CreatedAt, r.CreatedBy, r.Version, string(ports)); err != nil {
		return err
	}
	_, err := q.DB.ExecContext(ctx, `UPDATE node_core SET desired_revision=?,desired_version=?,input_sha256=?,last_error=NULL WHERE server_id=?`, r.Revision, r.Version, inputHash, r.ServerID)
	if err != nil {
		return err
	}
	// Keep exactly the latest twenty; MAX revision remains monotonic after cleanup.
	_, err = q.DB.ExecContext(ctx, `DELETE FROM config_revisions WHERE server_id=? AND revision NOT IN (SELECT revision FROM config_revisions WHERE server_id=? ORDER BY revision DESC LIMIT 20)`, r.ServerID, r.ServerID)
	return err
}
func (q ProxyQueries) CoreError(ctx context.Context, id int64, message *string) error {
	_, err := q.DB.ExecContext(ctx, "UPDATE node_core SET last_error=? WHERE server_id=?", message, id)
	return err
}
func (q ProxyQueries) SaveCoreState(ctx context.Context, id int64, s proto.CoreState, now int64, replaceError bool) error {
	listening, _ := json.Marshal(s.Listening)
	_, err := q.DB.ExecContext(ctx, `UPDATE node_core SET installed_version=?,running=?,applied_revision=?,config_sha256=?,listening=?,firewall=?,updated_at=?,last_error=CASE WHEN ? THEN ? ELSE last_error END WHERE server_id=?`, nullIfEmpty(s.InstalledVersion), s.Running, s.AppliedRevision, nullIfEmpty(s.ConfigSHA256), string(listening), nullIfEmpty(s.Firewall), now, replaceError, s.Error, id)
	return err
}
