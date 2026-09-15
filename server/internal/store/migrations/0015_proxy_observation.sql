-- +goose Up
CREATE TABLE proxy_observations (
  server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  instance_id TEXT NOT NULL,
  snapshot_json TEXT NOT NULL,
  received_at INTEGER NOT NULL,
  absent INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(server_id, instance_id)
);

-- +goose Down
DROP TABLE proxy_observations;
