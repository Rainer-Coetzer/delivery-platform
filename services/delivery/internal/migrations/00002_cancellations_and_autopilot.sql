-- +goose Up

-- Tombstones for orders cancelled before (or while) a driver was dispatched.
-- orders.cancelled and restaurant.order-ready live on different topics, so
-- Kafka gives no ordering between them: a cancel can arrive first and a
-- late order-ready would then dispatch a driver for a dead order. Every
-- assignment path checks this table first.
CREATE TABLE cancelled_orders (
    order_id     TEXT PRIMARY KEY,
    cancelled_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Timestamps the autopilot and delivery.completed's durationSeconds need.
-- deliveries.status gains the value 'CANCELLED' (TEXT column, no enum to alter).
ALTER TABLE deliveries ADD COLUMN picked_up_at TIMESTAMPTZ;
ALTER TABLE deliveries ADD COLUMN completed_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE deliveries DROP COLUMN completed_at;
ALTER TABLE deliveries DROP COLUMN picked_up_at;
DROP TABLE cancelled_orders;
