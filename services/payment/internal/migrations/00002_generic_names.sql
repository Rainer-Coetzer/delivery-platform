-- Phase C: generic domain names (ADR-0003). Renames only; no data is
-- dropped. The Go code expects these names from this release on.

-- +goose Up
ALTER TABLE outbox RENAME COLUMN order_id TO job_id;

ALTER TABLE payments RENAME COLUMN order_id TO job_id;
ALTER INDEX payments_order_idx RENAME TO payments_job_idx;
ALTER TABLE refunds RENAME COLUMN order_id TO job_id;

-- +goose Down
ALTER TABLE refunds RENAME COLUMN job_id TO order_id;
ALTER INDEX payments_job_idx RENAME TO payments_order_idx;
ALTER TABLE payments RENAME COLUMN job_id TO order_id;

ALTER TABLE outbox RENAME COLUMN job_id TO order_id;
