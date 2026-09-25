-- Phase C: generic domain names (ADR-0003). Renames only; no data is
-- dropped. The Go code expects these names from this release on.

-- +goose Up
ALTER TABLE outbox RENAME COLUMN order_id TO job_id;

ALTER TABLE drivers RENAME TO agents;
ALTER TABLE agents RENAME CONSTRAINT drivers_pkey TO agents_pkey;
ALTER INDEX drivers_location_gix RENAME TO agents_location_gix;
ALTER INDEX drivers_status_idx RENAME TO agents_status_idx;

ALTER TABLE deliveries RENAME TO assignments;
ALTER TABLE assignments RENAME COLUMN order_id TO job_id;
ALTER TABLE assignments RENAME COLUMN driver_id TO agent_id;
ALTER TABLE assignments RENAME COLUMN restaurant_id TO merchant_id;
ALTER TABLE assignments RENAME CONSTRAINT deliveries_pkey TO assignments_pkey;
ALTER TABLE assignments RENAME CONSTRAINT deliveries_order_id_key TO assignments_job_id_key;
ALTER TABLE assignments RENAME CONSTRAINT deliveries_driver_id_fkey TO assignments_agent_id_fkey;
ALTER INDEX deliveries_driver_idx RENAME TO assignments_agent_idx;
ALTER INDEX deliveries_status_idx RENAME TO assignments_status_idx;

ALTER TABLE pending_assignments RENAME COLUMN order_id TO job_id;
ALTER TABLE pending_assignments RENAME COLUMN restaurant_id TO merchant_id;

ALTER TABLE cancelled_orders RENAME TO cancelled_jobs;
ALTER TABLE cancelled_jobs RENAME COLUMN order_id TO job_id;
ALTER TABLE cancelled_jobs RENAME CONSTRAINT cancelled_orders_pkey TO cancelled_jobs_pkey;

-- +goose Down
ALTER TABLE cancelled_jobs RENAME CONSTRAINT cancelled_jobs_pkey TO cancelled_orders_pkey;
ALTER TABLE cancelled_jobs RENAME COLUMN job_id TO order_id;
ALTER TABLE cancelled_jobs RENAME TO cancelled_orders;

ALTER TABLE pending_assignments RENAME COLUMN merchant_id TO restaurant_id;
ALTER TABLE pending_assignments RENAME COLUMN job_id TO order_id;

ALTER INDEX assignments_status_idx RENAME TO deliveries_status_idx;
ALTER INDEX assignments_agent_idx RENAME TO deliveries_driver_idx;
ALTER TABLE assignments RENAME CONSTRAINT assignments_agent_id_fkey TO deliveries_driver_id_fkey;
ALTER TABLE assignments RENAME CONSTRAINT assignments_job_id_key TO deliveries_order_id_key;
ALTER TABLE assignments RENAME CONSTRAINT assignments_pkey TO deliveries_pkey;
ALTER TABLE assignments RENAME COLUMN merchant_id TO restaurant_id;
ALTER TABLE assignments RENAME COLUMN agent_id TO driver_id;
ALTER TABLE assignments RENAME COLUMN job_id TO order_id;
ALTER TABLE assignments RENAME TO deliveries;

ALTER INDEX agents_status_idx RENAME TO drivers_status_idx;
ALTER INDEX agents_location_gix RENAME TO drivers_location_gix;
ALTER TABLE agents RENAME CONSTRAINT agents_pkey TO drivers_pkey;
ALTER TABLE agents RENAME TO drivers;

ALTER TABLE outbox RENAME COLUMN job_id TO order_id;
