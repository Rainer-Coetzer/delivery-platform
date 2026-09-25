-- Phase C: generic domain names (ADR-0003). Renames only; no data is
-- dropped. The Go code expects these names from this release on.

-- +goose Up
ALTER TABLE notifications RENAME COLUMN order_id TO job_id;

-- Inbox names changed with the roles; rewrite them so old notifications
-- stay readable by the same people.
UPDATE notifications SET recipient = 'merchant:' || substr(recipient, length('restaurant:') + 1) WHERE recipient LIKE 'restaurant:%';
UPDATE notifications SET recipient = 'agent:' || substr(recipient, length('driver:') + 1) WHERE recipient LIKE 'driver:%';
UPDATE notifications SET recipient = 'job:' || substr(recipient, length('order:') + 1) WHERE recipient LIKE 'order:%';

-- +goose Down
UPDATE notifications SET recipient = 'order:' || substr(recipient, length('job:') + 1) WHERE recipient LIKE 'job:%';
UPDATE notifications SET recipient = 'driver:' || substr(recipient, length('agent:') + 1) WHERE recipient LIKE 'agent:%';
UPDATE notifications SET recipient = 'restaurant:' || substr(recipient, length('merchant:') + 1) WHERE recipient LIKE 'merchant:%';

ALTER TABLE notifications RENAME COLUMN job_id TO order_id;
