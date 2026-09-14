-- +goose Up
ALTER TABLE subscribers ADD COLUMN warn80_sent INTEGER NOT NULL DEFAULT 0 CHECK(warn80_sent IN (0,1));
-- A calendar identity does not move when the panel timezone changes. Legacy
-- timestamps are backfilled by startup using the previously effective timezone.
ALTER TABLE subscribers ADD COLUMN period_date TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE subscribers DROP COLUMN period_date;
ALTER TABLE subscribers DROP COLUMN warn80_sent;
