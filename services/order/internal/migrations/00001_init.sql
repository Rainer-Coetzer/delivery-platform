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

CREATE TABLE orders (
    id                 TEXT PRIMARY KEY,
    customer_id        TEXT NOT NULL,
    customer_name      TEXT NOT NULL,
    restaurant_id      TEXT NOT NULL,
    restaurant_name    TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'CREATED',
    paid               BOOLEAN NOT NULL DEFAULT false,
    restaurant_accepted BOOLEAN NOT NULL DEFAULT false,
    subtotal_cents     BIGINT NOT NULL,
    delivery_fee_cents BIGINT NOT NULL,
    surge_multiplier   DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    total_cents        BIGINT NOT NULL,
    payment_method     TEXT NOT NULL,
    pickup_lat         DOUBLE PRECISION NOT NULL,
    pickup_lng         DOUBLE PRECISION NOT NULL,
    pickup_label       TEXT,
    dropoff_lat        DOUBLE PRECISION NOT NULL,
    dropoff_lng        DOUBLE PRECISION NOT NULL,
    dropoff_label      TEXT,
    driver_id          TEXT,
    cancel_reason      TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_customer_idx ON orders (customer_id, created_at DESC);
CREATE INDEX orders_restaurant_idx ON orders (restaurant_id, created_at DESC);

CREATE TABLE order_items (
    id               BIGSERIAL PRIMARY KEY,
    order_id         TEXT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    menu_item_id     TEXT NOT NULL,
    name             TEXT NOT NULL,
    quantity         INT NOT NULL,
    unit_price_cents BIGINT NOT NULL
);
CREATE INDEX order_items_order_idx ON order_items (order_id);

CREATE TABLE order_status_history (
    id         BIGSERIAL PRIMARY KEY,
    order_id   TEXT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    reason      TEXT,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX order_status_history_order_idx ON order_status_history (order_id, changed_at);

-- +goose Down
DROP TABLE order_status_history;
DROP TABLE order_items;
DROP TABLE orders;
DROP TABLE outbox;
DROP TABLE processed_events;
