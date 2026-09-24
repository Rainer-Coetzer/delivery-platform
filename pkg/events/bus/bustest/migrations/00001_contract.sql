-- +goose Up
-- The same idempotency table every module has, plus a counter the dedupe
-- test increments only for events seen the first time.
CREATE TABLE processed_events (
    event_id     TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE effects (
    event_id TEXT NOT NULL,
    applied  INT NOT NULL
);
-- +goose Down
DROP TABLE effects;
DROP TABLE processed_events;
