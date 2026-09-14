-- +goose Up
CREATE TABLE inbounds (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  protocol TEXT NOT NULL CHECK(protocol IN ('vless','shadowsocks','hysteria2','tuic')),
  listen_port INTEGER NOT NULL CHECK(listen_port BETWEEN 1 AND 65535),
  settings TEXT NOT NULL CHECK(json_valid(settings)),
  remark TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  UNIQUE(server_id,tag)
);
CREATE INDEX idx_inbounds_server ON inbounds(server_id,id);
-- Reserve transport/port even for disabled inbounds. Database triggers also
-- protect against concurrent writers and future callers outside the HTTP API.
-- +goose StatementBegin
CREATE TRIGGER inbounds_port_insert BEFORE INSERT ON inbounds
WHEN EXISTS (SELECT 1 FROM inbounds WHERE server_id=NEW.server_id AND listen_port=NEW.listen_port
  AND (protocol='shadowsocks' OR NEW.protocol='shadowsocks'
       OR (protocol='vless' AND NEW.protocol='vless')
       OR (protocol IN ('hysteria2','tuic') AND NEW.protocol IN ('hysteria2','tuic'))))
BEGIN SELECT RAISE(ABORT,'proxy_port_conflict'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER inbounds_port_update BEFORE UPDATE OF server_id,protocol,listen_port ON inbounds
WHEN EXISTS (SELECT 1 FROM inbounds WHERE id<>NEW.id AND server_id=NEW.server_id AND listen_port=NEW.listen_port
  AND (protocol='shadowsocks' OR NEW.protocol='shadowsocks'
       OR (protocol='vless' AND NEW.protocol='vless')
       OR (protocol IN ('hysteria2','tuic') AND NEW.protocol IN ('hysteria2','tuic'))))
BEGIN SELECT RAISE(ABORT,'proxy_port_conflict'); END;
-- +goose StatementEnd
CREATE TABLE certs (
  server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
  sni TEXT NOT NULL, cert_pem TEXT NOT NULL, key_pem TEXT NOT NULL,
  fingerprint_sha256 TEXT NOT NULL, not_after INTEGER NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE node_core (
  server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
  core TEXT NOT NULL DEFAULT 'sing-box', desired_version TEXT, installed_version TEXT,
  running INTEGER NOT NULL DEFAULT 0, applied_revision INTEGER NOT NULL DEFAULT 0,
  desired_revision INTEGER NOT NULL DEFAULT 0, config_sha256 TEXT, listening TEXT,
  firewall TEXT, last_error TEXT, updated_at INTEGER
);
INSERT INTO node_core(server_id) SELECT id FROM servers;
-- +goose StatementBegin
CREATE TRIGGER servers_init_core AFTER INSERT ON servers
BEGIN INSERT INTO node_core(server_id) VALUES(NEW.id); END;
-- +goose StatementEnd
CREATE TABLE config_revisions (
  server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL CHECK(revision>0), config_json TEXT NOT NULL, sha256 TEXT NOT NULL,
  created_at INTEGER NOT NULL, created_by TEXT NOT NULL,
  PRIMARY KEY(server_id,revision)
);
CREATE TABLE node_advanced (
  server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
  extra_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra_json)), updated_at INTEGER
);
CREATE TABLE subscribers (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  auto_disabled TEXT NOT NULL DEFAULT 'none' CHECK(auto_disabled IN ('none','quota','expired')),
  sub_token TEXT NOT NULL UNIQUE, uuid TEXT NOT NULL UNIQUE, password TEXT NOT NULL, ss_user_key TEXT NOT NULL,
  traffic_limit INTEGER NOT NULL DEFAULT 0 CHECK(traffic_limit>=0), traffic_used INTEGER NOT NULL DEFAULT 0 CHECK(traffic_used>=0),
  reset_day INTEGER NOT NULL DEFAULT 0 CHECK(reset_day BETWEEN 0 AND 31),
  period_start INTEGER NOT NULL, expire_at TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE subscriber_assignments (
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
  PRIMARY KEY(subscriber_id,inbound_id)
);
CREATE INDEX idx_assignment_inbound ON subscriber_assignments(inbound_id,subscriber_id);
-- Traffic intentionally survives node deletion, so a removed node cannot refund quota.
CREATE TABLE subscriber_traffic (
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  server_id INTEGER NOT NULL, period_start INTEGER NOT NULL,
  up_bytes INTEGER NOT NULL DEFAULT 0 CHECK(up_bytes>=0), down_bytes INTEGER NOT NULL DEFAULT 0 CHECK(down_bytes>=0),
  PRIMARY KEY(subscriber_id,server_id,period_start)
);
CREATE TABLE subscriber_traffic_daily (
  subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  date TEXT NOT NULL, up_bytes INTEGER NOT NULL DEFAULT 0 CHECK(up_bytes>=0), down_bytes INTEGER NOT NULL DEFAULT 0 CHECK(down_bytes>=0),
  PRIMARY KEY(subscriber_id,date)
);

-- +goose Down
DROP TABLE subscriber_traffic_daily;
DROP TABLE subscriber_traffic;
DROP TABLE subscriber_assignments;
DROP TABLE subscribers;
DROP TABLE node_advanced;
DROP TABLE config_revisions;
DROP TRIGGER servers_init_core;
DROP TABLE node_core;
DROP TABLE certs;
DROP TRIGGER inbounds_port_update;
DROP TRIGGER inbounds_port_insert;
DROP TABLE inbounds;
