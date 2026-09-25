-- Phase C: generic domain names (ADR-0003). Renames only; no data is
-- dropped. The Go code expects these names from this release on.

-- +goose Up
ALTER TABLE orders_projection RENAME TO jobs_projection;
ALTER TABLE jobs_projection RENAME COLUMN order_id TO job_id;
ALTER TABLE jobs_projection RENAME COLUMN restaurant_id TO merchant_id;
ALTER TABLE jobs_projection RENAME COLUMN restaurant_name TO merchant_name;
ALTER TABLE jobs_projection RENAME COLUMN driver_id TO agent_id;
ALTER TABLE jobs_projection RENAME COLUMN delivered_at TO completed_at;
ALTER TABLE jobs_projection RENAME CONSTRAINT orders_projection_pkey TO jobs_projection_pkey;
ALTER INDEX orders_projection_restaurant_idx RENAME TO jobs_projection_merchant_idx;
ALTER INDEX orders_projection_driver_idx RENAME TO jobs_projection_agent_idx;
ALTER INDEX orders_projection_status_idx RENAME TO jobs_projection_status_idx;

-- +goose Down
ALTER INDEX jobs_projection_status_idx RENAME TO orders_projection_status_idx;
ALTER INDEX jobs_projection_agent_idx RENAME TO orders_projection_driver_idx;
ALTER INDEX jobs_projection_merchant_idx RENAME TO orders_projection_restaurant_idx;
ALTER TABLE jobs_projection RENAME CONSTRAINT jobs_projection_pkey TO orders_projection_pkey;
ALTER TABLE jobs_projection RENAME COLUMN completed_at TO delivered_at;
ALTER TABLE jobs_projection RENAME COLUMN agent_id TO driver_id;
ALTER TABLE jobs_projection RENAME COLUMN merchant_name TO restaurant_name;
ALTER TABLE jobs_projection RENAME COLUMN merchant_id TO restaurant_id;
ALTER TABLE jobs_projection RENAME COLUMN job_id TO order_id;
ALTER TABLE jobs_projection RENAME TO orders_projection;
