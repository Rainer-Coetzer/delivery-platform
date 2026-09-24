-- +goose Up
CREATE TABLE processed_events (
    event_id   TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE outbox (
    id            BIGSERIAL PRIMARY KEY,
    event_id      TEXT NOT NULL UNIQUE,
    event_type    TEXT NOT NULL,
    order_id      TEXT,
    partition_key TEXT NOT NULL,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    sent_at       TIMESTAMPTZ
);
CREATE INDEX outbox_unsent_idx ON outbox (id) WHERE sent_at IS NULL;

-- idempotency_key defaults to the order id: this system charges an order
-- at most once no matter how many times orders.created is redelivered.
CREATE TABLE payments (
    id               TEXT PRIMARY KEY,
    order_id         TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL UNIQUE,
    customer_id      TEXT NOT NULL,
    amount_cents     BIGINT NOT NULL,
    method           TEXT NOT NULL,
    status           TEXT NOT NULL, -- COMPLETED, FAILED, REFUNDED
    failure_reason   TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payments_order_idx ON payments (order_id);

CREATE TABLE refunds (
    id           TEXT PRIMARY KEY,
    payment_id   TEXT NOT NULL REFERENCES payments (id),
    order_id     TEXT NOT NULL,
    amount_cents BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE refunds;
DROP TABLE payments;
DROP TABLE outbox;
DROP TABLE processed_events;
