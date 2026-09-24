-- +goose Up
CREATE TABLE processed_events (
    event_id   TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- recipient is "user:<id>" or "restaurant:<id>" so one table serves every
-- audience; the gateway's WebSocket hub and the Flutter apps both read from
-- here (via GET /notifications?recipient=...) as well as receiving the live
-- push over the socket.
CREATE TABLE notifications (
    id         TEXT PRIMARY KEY,
    recipient  TEXT NOT NULL,
    order_id   TEXT,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    channel    TEXT NOT NULL DEFAULT 'in-app',
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notifications_recipient_idx ON notifications (recipient, created_at DESC);

-- +goose Down
DROP TABLE notifications;
DROP TABLE processed_events;
