-- +goose Up
ALTER TABLE config_revisions ADD COLUMN version TEXT NOT NULL DEFAULT '';
ALTER TABLE config_revisions ADD COLUMN ports TEXT NOT NULL DEFAULT '[]';
ALTER TABLE node_core ADD COLUMN input_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE subscribers ADD COLUMN usage_epoch INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE subscribers DROP COLUMN usage_epoch;
ALTER TABLE node_core DROP COLUMN input_sha256;
ALTER TABLE config_revisions DROP COLUMN ports;
ALTER TABLE config_revisions DROP COLUMN version;
