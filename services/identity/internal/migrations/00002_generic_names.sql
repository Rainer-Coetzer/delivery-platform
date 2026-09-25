-- Phase C: generic domain names (ADR-0003). Renames only; no data is
-- dropped. The Go code expects these names from this release on.

-- +goose Up
ALTER TABLE order_history RENAME TO job_history;
ALTER TABLE job_history RENAME COLUMN order_id TO job_id;
ALTER TABLE job_history RENAME COLUMN restaurant_name TO merchant_name;
ALTER TABLE job_history RENAME CONSTRAINT order_history_pkey TO job_history_pkey;
ALTER INDEX order_history_customer_idx RENAME TO job_history_customer_idx;

-- Role names are in every issued JWT too: tokens minted before the upgrade
-- carry the old role and are refused until the user logs in again.
UPDATE users SET role = 'merchant' WHERE role = 'restaurant';
UPDATE users SET role = 'agent' WHERE role = 'driver';

-- +goose Down
UPDATE users SET role = 'driver' WHERE role = 'agent';
UPDATE users SET role = 'restaurant' WHERE role = 'merchant';

ALTER INDEX job_history_customer_idx RENAME TO order_history_customer_idx;
ALTER TABLE job_history RENAME CONSTRAINT job_history_pkey TO order_history_pkey;
ALTER TABLE job_history RENAME COLUMN merchant_name TO restaurant_name;
ALTER TABLE job_history RENAME COLUMN job_id TO order_id;
ALTER TABLE job_history RENAME TO order_history;
