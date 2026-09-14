-- +goose Up
CREATE TABLE alert_rules (
 kind TEXT PRIMARY KEY, params TEXT NOT NULL DEFAULT '{}',
 enabled INTEGER NOT NULL DEFAULT 1, updated_at INTEGER NOT NULL
);
INSERT INTO alert_rules(kind,params,updated_at) VALUES
 ('server.offline','{"minutes":3}',unixepoch()),
 ('server.cpu','{"percent":90,"minutes":10}',unixepoch()),
 ('server.mem','{"percent":90,"minutes":10}',unixepoch()),
 ('server.disk','{"percent":90,"minutes":10}',unixepoch()),
 ('server.traffic','{"percents":[80,90,100]}',unixepoch()),
 ('server.expire','{"days":[7,3,1]}',unixepoch()),
 ('ping.loss','{"percent":30}',unixepoch()),
 ('subscriber.quota','{"percents":[80,100]}',unixepoch()),
 ('subscriber.expired','{}',unixepoch()),
 ('core.apply_failed','{}',unixepoch()),
 ('core.down','{"minutes":1}',unixepoch());
CREATE TABLE notify_channels (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL,
 config TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL
);
CREATE TABLE alert_events (
 id INTEGER PRIMARY KEY, rule_kind TEXT NOT NULL, target_type TEXT NOT NULL, target_id INTEGER,
 level TEXT NOT NULL, title TEXT NOT NULL, message TEXT NOT NULL,
 fired_at INTEGER NOT NULL, resolved_at INTEGER, notified_at INTEGER, dedupe_key TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_alert_events_open ON alert_events(dedupe_key) WHERE resolved_at IS NULL;
CREATE INDEX idx_alert_events_fired ON alert_events(fired_at);
CREATE INDEX idx_alert_events_key ON alert_events(dedupe_key,fired_at);
-- The outbox records each channel independently: restart does not lose attempts,
-- and a successful channel is never retried because another channel failed.
CREATE TABLE alert_deliveries (
 event_id INTEGER NOT NULL REFERENCES alert_events(id) ON DELETE CASCADE,
 channel_id INTEGER NOT NULL REFERENCES notify_channels(id) ON DELETE CASCADE,
 recovery INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
 next_at INTEGER NOT NULL, sent_at INTEGER, last_error TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(event_id,channel_id,recovery)
);
CREATE TABLE alert_once (
 dedupe_key TEXT NOT NULL, occurrence TEXT NOT NULL, fired_at INTEGER NOT NULL,
 PRIMARY KEY(dedupe_key,occurrence)
);
INSERT OR IGNORE INTO settings(key,value) VALUES('alert.cooldown_minutes','30');

-- +goose Down
DELETE FROM settings WHERE key='alert.cooldown_minutes';
DROP TABLE alert_once;
DROP TABLE alert_deliveries;
DROP TABLE alert_events;
DROP TABLE notify_channels;
DROP TABLE alert_rules;
