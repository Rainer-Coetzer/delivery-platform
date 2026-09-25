-- Phase D: tenant_id, the path to running several brands on one install
-- (docs/MULTI_TENANCY.md). Every row belongs to the install's one tenant
-- (config/brand.yaml tenant_id); nothing reads the column yet. No default
-- in the schema, and never empty: a write that forgets its tenant fails
-- here instead of landing in someone else's.

-- +goose Up
ALTER TABLE jobs ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE jobs ALTER COLUMN tenant_id DROP DEFAULT;
ALTER TABLE jobs ADD CONSTRAINT jobs_tenant_id_set CHECK (tenant_id <> '');
CREATE INDEX jobs_tenant_idx ON jobs (tenant_id);

-- +goose Down
DROP INDEX jobs_tenant_idx;
ALTER TABLE jobs DROP CONSTRAINT jobs_tenant_id_set;
ALTER TABLE jobs DROP COLUMN tenant_id;
