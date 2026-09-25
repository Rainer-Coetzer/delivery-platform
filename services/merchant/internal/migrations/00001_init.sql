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

CREATE TABLE restaurants (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    opens_at   TIME NOT NULL DEFAULT '00:00',
    closes_at  TIME NOT NULL DEFAULT '23:59',
    is_open    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE menu_items (
    id            TEXT PRIMARY KEY,
    restaurant_id TEXT NOT NULL REFERENCES restaurants (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    price_cents   BIGINT NOT NULL,
    stock_qty     INT NOT NULL DEFAULT 0,
    available     BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX menu_items_restaurant_idx ON menu_items (restaurant_id);

-- A local cache of just enough order data to decide accept/reject, release
-- stock on cancellation, and emit restaurant.order-ready with the pickup and
-- dropoff coordinates the delivery service needs — without ever having to
-- call the Order service synchronously.
CREATE TABLE restaurant_orders (
    order_id      TEXT PRIMARY KEY,
    restaurant_id TEXT NOT NULL REFERENCES restaurants (id),
    status        TEXT NOT NULL DEFAULT 'PENDING',
    items         JSONB NOT NULL,
    pickup_lat    DOUBLE PRECISION NOT NULL,
    pickup_lng    DOUBLE PRECISION NOT NULL,
    dropoff_lat   DOUBLE PRECISION NOT NULL,
    dropoff_lng   DOUBLE PRECISION NOT NULL,
    reject_reason TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX restaurant_orders_restaurant_idx ON restaurant_orders (restaurant_id, created_at DESC);

-- +goose Down
DROP TABLE restaurant_orders;
DROP TABLE menu_items;
DROP TABLE restaurants;
DROP TABLE outbox;
DROP TABLE processed_events;
