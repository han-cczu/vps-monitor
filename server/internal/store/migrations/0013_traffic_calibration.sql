-- +goose Up
ALTER TABLE traffic_periods ADD COLUMN calibration_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_periods ADD COLUMN calibrated_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE traffic_periods DROP COLUMN calibrated_at;
ALTER TABLE traffic_periods DROP COLUMN calibration_revision;
