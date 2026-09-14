-- +goose Up
CREATE TABLE totp_used (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 step INTEGER NOT NULL,
 code_hash TEXT NOT NULL,
 used_at INTEGER NOT NULL,
 PRIMARY KEY(user_id, step)
);
CREATE INDEX idx_totp_used_at ON totp_used(used_at);
CREATE INDEX idx_audit_filter ON audit_log(actor, action, ts);
-- +goose Down
DROP INDEX IF EXISTS idx_audit_filter;
DROP TABLE IF EXISTS totp_used;
