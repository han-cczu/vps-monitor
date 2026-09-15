-- +goose Up
-- Existing nodes retain their monthly reset rule and all recorded usage.
ALTER TABLE servers ADD COLUMN traffic_reset_mode TEXT NOT NULL DEFAULT 'monthly' CHECK (traffic_reset_mode IN ('monthly', 'days'));
ALTER TABLE traffic_periods ADD COLUMN next_reset INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE traffic_periods DROP COLUMN next_reset;
ALTER TABLE servers DROP COLUMN traffic_reset_mode;
