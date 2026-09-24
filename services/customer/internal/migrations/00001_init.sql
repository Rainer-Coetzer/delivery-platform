-- +goose Up
CREATE TABLE processed_events (
    event_id   TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- This service consumes but never produces outbox events of its own, so it
-- has processed_events for idempotency but no outbox table.

-- Users are centralized here for every role (customer, restaurant, driver,
-- admin) so the API Gateway has one place to verify credentials before
-- issuing a JWT that the other services trust. ref_id links a
-- restaurant/driver account to its row in that service's own database.
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL, -- customer, restaurant, driver, admin
    ref_id        TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE addresses (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    label      TEXT NOT NULL,
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX addresses_user_idx ON addresses (user_id);

-- A read-optimized projection of order history built from
-- orders.created / orders.status-changed, so a customer's order list never
-- needs a synchronous call to the Order service.
CREATE TABLE order_history (
    order_id        TEXT PRIMARY KEY,
    customer_id     TEXT NOT NULL,
    restaurant_name TEXT NOT NULL,
    status          TEXT NOT NULL,
    total_cents     BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX order_history_customer_idx ON order_history (customer_id, created_at DESC);

-- +goose Down
DROP TABLE order_history;
DROP TABLE addresses;
DROP TABLE users;
DROP TABLE processed_events;
