-- Phase C: generic domain names (ADR-0003). Renames only; no data is
-- dropped. The Go code expects these names from this release on.

-- +goose Up
ALTER TABLE outbox RENAME COLUMN order_id TO job_id;

ALTER TABLE orders RENAME TO jobs;
ALTER TABLE jobs RENAME COLUMN restaurant_id TO merchant_id;
ALTER TABLE jobs RENAME COLUMN restaurant_name TO merchant_name;
ALTER TABLE jobs RENAME COLUMN restaurant_accepted TO merchant_accepted;
ALTER TABLE jobs RENAME COLUMN driver_id TO agent_id;
ALTER TABLE jobs RENAME CONSTRAINT orders_pkey TO jobs_pkey;
ALTER INDEX orders_customer_idx RENAME TO jobs_customer_idx;
ALTER INDEX orders_restaurant_idx RENAME TO jobs_merchant_idx;

ALTER TABLE order_items RENAME TO job_lines;
ALTER TABLE job_lines RENAME COLUMN order_id TO job_id;
ALTER TABLE job_lines RENAME COLUMN menu_item_id TO catalogue_item_id;
ALTER TABLE job_lines RENAME CONSTRAINT order_items_pkey TO job_lines_pkey;
ALTER TABLE job_lines RENAME CONSTRAINT order_items_order_id_fkey TO job_lines_job_id_fkey;
ALTER INDEX order_items_order_idx RENAME TO job_lines_job_idx;
ALTER SEQUENCE order_items_id_seq RENAME TO job_lines_id_seq;

ALTER TABLE order_status_history RENAME TO job_events;
ALTER TABLE job_events RENAME COLUMN order_id TO job_id;
ALTER TABLE job_events RENAME CONSTRAINT order_status_history_pkey TO job_events_pkey;
ALTER TABLE job_events RENAME CONSTRAINT order_status_history_order_id_fkey TO job_events_job_id_fkey;
ALTER INDEX order_status_history_order_idx RENAME TO job_events_job_idx;
ALTER SEQUENCE order_status_history_id_seq RENAME TO job_events_id_seq;

-- +goose Down
ALTER SEQUENCE job_events_id_seq RENAME TO order_status_history_id_seq;
ALTER INDEX job_events_job_idx RENAME TO order_status_history_order_idx;
ALTER TABLE job_events RENAME CONSTRAINT job_events_job_id_fkey TO order_status_history_order_id_fkey;
ALTER TABLE job_events RENAME CONSTRAINT job_events_pkey TO order_status_history_pkey;
ALTER TABLE job_events RENAME COLUMN job_id TO order_id;
ALTER TABLE job_events RENAME TO order_status_history;

ALTER SEQUENCE job_lines_id_seq RENAME TO order_items_id_seq;
ALTER INDEX job_lines_job_idx RENAME TO order_items_order_idx;
ALTER TABLE job_lines RENAME CONSTRAINT job_lines_job_id_fkey TO order_items_order_id_fkey;
ALTER TABLE job_lines RENAME CONSTRAINT job_lines_pkey TO order_items_pkey;
ALTER TABLE job_lines RENAME COLUMN catalogue_item_id TO menu_item_id;
ALTER TABLE job_lines RENAME COLUMN job_id TO order_id;
ALTER TABLE job_lines RENAME TO order_items;

ALTER INDEX jobs_merchant_idx RENAME TO orders_restaurant_idx;
ALTER INDEX jobs_customer_idx RENAME TO orders_customer_idx;
ALTER TABLE jobs RENAME CONSTRAINT jobs_pkey TO orders_pkey;
ALTER TABLE jobs RENAME COLUMN agent_id TO driver_id;
ALTER TABLE jobs RENAME COLUMN merchant_accepted TO restaurant_accepted;
ALTER TABLE jobs RENAME COLUMN merchant_name TO restaurant_name;
ALTER TABLE jobs RENAME COLUMN merchant_id TO restaurant_id;
ALTER TABLE jobs RENAME TO orders;

ALTER TABLE outbox RENAME COLUMN job_id TO order_id;
