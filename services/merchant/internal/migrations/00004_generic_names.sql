-- Phase C: generic domain names (ADR-0003). Renames only; no data is
-- dropped. The Go code expects these names from this release on.

-- +goose Up
ALTER TABLE outbox RENAME COLUMN order_id TO job_id;

ALTER TABLE restaurants RENAME TO merchants;
ALTER TABLE merchants RENAME CONSTRAINT restaurants_pkey TO merchants_pkey;

ALTER TABLE menu_items RENAME TO catalogue_items;
ALTER TABLE catalogue_items RENAME COLUMN restaurant_id TO merchant_id;
ALTER TABLE catalogue_items RENAME CONSTRAINT menu_items_pkey TO catalogue_items_pkey;
ALTER TABLE catalogue_items RENAME CONSTRAINT menu_items_restaurant_id_fkey TO catalogue_items_merchant_id_fkey;
ALTER INDEX menu_items_restaurant_idx RENAME TO catalogue_items_merchant_idx;

ALTER TABLE restaurant_orders RENAME TO merchant_jobs;
ALTER TABLE merchant_jobs RENAME COLUMN order_id TO job_id;
ALTER TABLE merchant_jobs RENAME COLUMN restaurant_id TO merchant_id;
ALTER TABLE merchant_jobs RENAME CONSTRAINT restaurant_orders_pkey TO merchant_jobs_pkey;
ALTER TABLE merchant_jobs RENAME CONSTRAINT restaurant_orders_restaurant_id_fkey TO merchant_jobs_merchant_id_fkey;
ALTER INDEX restaurant_orders_restaurant_idx RENAME TO merchant_jobs_merchant_idx;

-- items is the job's lines as the event carried them. The key changed
-- with the rename (menuItemId -> catalogueItemId); without this, cancelling
-- a job accepted before the upgrade would restore stock to no item at all.
UPDATE merchant_jobs SET items = coalesce((
    SELECT jsonb_agg(CASE WHEN e ? 'menuItemId'
                          THEN (e - 'menuItemId') || jsonb_build_object('catalogueItemId', e -> 'menuItemId')
                          ELSE e END)
    FROM jsonb_array_elements(items) AS e), '[]'::jsonb)
WHERE jsonb_typeof(items) = 'array';

-- +goose Down
UPDATE merchant_jobs SET items = coalesce((
    SELECT jsonb_agg(CASE WHEN e ? 'catalogueItemId'
                          THEN (e - 'catalogueItemId') || jsonb_build_object('menuItemId', e -> 'catalogueItemId')
                          ELSE e END)
    FROM jsonb_array_elements(items) AS e), '[]'::jsonb)
WHERE jsonb_typeof(items) = 'array';

ALTER INDEX merchant_jobs_merchant_idx RENAME TO restaurant_orders_restaurant_idx;
ALTER TABLE merchant_jobs RENAME CONSTRAINT merchant_jobs_merchant_id_fkey TO restaurant_orders_restaurant_id_fkey;
ALTER TABLE merchant_jobs RENAME CONSTRAINT merchant_jobs_pkey TO restaurant_orders_pkey;
ALTER TABLE merchant_jobs RENAME COLUMN merchant_id TO restaurant_id;
ALTER TABLE merchant_jobs RENAME COLUMN job_id TO order_id;
ALTER TABLE merchant_jobs RENAME TO restaurant_orders;

ALTER INDEX catalogue_items_merchant_idx RENAME TO menu_items_restaurant_idx;
ALTER TABLE catalogue_items RENAME CONSTRAINT catalogue_items_merchant_id_fkey TO menu_items_restaurant_id_fkey;
ALTER TABLE catalogue_items RENAME CONSTRAINT catalogue_items_pkey TO menu_items_pkey;
ALTER TABLE catalogue_items RENAME COLUMN merchant_id TO restaurant_id;
ALTER TABLE catalogue_items RENAME TO menu_items;

ALTER TABLE merchants RENAME CONSTRAINT merchants_pkey TO restaurants_pkey;
ALTER TABLE merchants RENAME TO restaurants;

ALTER TABLE outbox RENAME COLUMN job_id TO order_id;
