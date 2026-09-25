-- Phase D: tenant_id, the path to running several brands on one install
-- (docs/MULTI_TENANCY.md). Every row belongs to the install's one tenant
-- (config/brand.yaml tenant_id); nothing reads the column yet. No default
-- in the schema, and never empty: a write that forgets its tenant fails
-- here instead of landing in someone else's.

-- +goose Up
ALTER TABLE merchants ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE merchants ALTER COLUMN tenant_id DROP DEFAULT;
ALTER TABLE merchants ADD CONSTRAINT merchants_tenant_id_set CHECK (tenant_id <> '');
CREATE INDEX merchants_tenant_idx ON merchants (tenant_id);

-- +goose Down
DROP INDEX merchants_tenant_idx;
ALTER TABLE merchants DROP CONSTRAINT merchants_tenant_id_set;
ALTER TABLE merchants DROP COLUMN tenant_id;
