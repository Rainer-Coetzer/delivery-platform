-- +goose Up
CREATE TABLE processed_events (
    event_id   TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A CQRS read model built entirely from consumed events: every report this
-- service serves is just a query over this one table, so there is nothing
-- to keep in sync by hand.
CREATE TABLE orders_projection (
    order_id        TEXT PRIMARY KEY,
    restaurant_id   TEXT NOT NULL,
    restaurant_name TEXT NOT NULL,
    customer_id     TEXT NOT NULL,
    driver_id       TEXT,
    status          TEXT NOT NULL,
    total_cents     BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    delivered_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_projection_restaurant_idx ON orders_projection (restaurant_id);
CREATE INDEX orders_projection_driver_idx ON orders_projection (driver_id);
CREATE INDEX orders_projection_status_idx ON orders_projection (status);

-- +goose Down
DROP TABLE orders_projection;
DROP TABLE processed_events;
