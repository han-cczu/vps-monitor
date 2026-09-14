-- +goose Up
-- Keep already-applied migrations immutable. Legacy timestamps are backfilled
-- by Enforcer.Initialize using the panel timezone before HTTP accepts changes.
ALTER TABLE subscribers ADD COLUMN period_date TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE subscribers DROP COLUMN period_date;
