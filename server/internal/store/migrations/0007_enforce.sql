-- +goose Up
ALTER TABLE subscribers ADD COLUMN warn80_sent INTEGER NOT NULL DEFAULT 0 CHECK(warn80_sent IN (0,1));

-- +goose Down
ALTER TABLE subscribers DROP COLUMN warn80_sent;
