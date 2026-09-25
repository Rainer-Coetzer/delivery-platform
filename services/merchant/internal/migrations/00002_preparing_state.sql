-- +goose Up

-- When the Order service confirmed the order (paid + accepted) and the
-- kitchen started. restaurant_orders.status gains 'PREPARING' between
-- ACCEPTED and READY (TEXT column, no enum to alter). Only a PREPARING order
-- can be marked ready: before this, READY could be sent for an unpaid order,
-- which the Order service then rejected and dead-lettered (audit P1).
ALTER TABLE restaurant_orders ADD COLUMN preparing_since TIMESTAMPTZ;

-- +goose Down
ALTER TABLE restaurant_orders DROP COLUMN preparing_since;
