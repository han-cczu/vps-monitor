-- +goose Up
CREATE TABLE traffic_counters (
  server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
  last_rx INTEGER NOT NULL, last_tx INTEGER NOT NULL, last_ts INTEGER NOT NULL
);
CREATE TABLE traffic_periods (
  server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  period_start INTEGER NOT NULL, period_end INTEGER,
  in_bytes INTEGER NOT NULL DEFAULT 0, out_bytes INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (server_id, period_start)
);
CREATE UNIQUE INDEX idx_traffic_current ON traffic_periods(server_id) WHERE period_end IS NULL;
CREATE TABLE billing_reminders (
  server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  date TEXT NOT NULL, days INTEGER NOT NULL,
  PRIMARY KEY (server_id, date, days)
);

-- +goose Down
DROP TABLE billing_reminders;
DROP TABLE traffic_periods;
DROP TABLE traffic_counters;
