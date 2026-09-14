-- +goose Up
ALTER TABLE subscribers ADD COLUMN kind TEXT NOT NULL DEFAULT 'user' CHECK(kind IN ('user','relay'));
CREATE UNIQUE INDEX idx_subscriber_relay_name ON subscribers(name) WHERE kind='relay';

-- +goose Down
DROP INDEX idx_subscriber_relay_name;
ALTER TABLE subscribers DROP COLUMN kind;
