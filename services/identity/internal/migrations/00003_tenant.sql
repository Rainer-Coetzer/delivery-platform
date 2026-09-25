-- Phase D: tenant_id, the path to running several brands on one install
-- (docs/MULTI_TENANCY.md). Every row belongs to the install's one tenant
-- (config/brand.yaml tenant_id); nothing reads the column yet. No default
-- in the schema, and never empty: a write that forgets its tenant fails
-- here instead of landing in someone else's.

-- +goose Up
ALTER TABLE users ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE users ALTER COLUMN tenant_id DROP DEFAULT;
ALTER TABLE users ADD CONSTRAINT users_tenant_id_set CHECK (tenant_id <> '');
CREATE INDEX users_tenant_idx ON users (tenant_id);

-- +goose Down
DROP INDEX users_tenant_idx;
ALTER TABLE users DROP CONSTRAINT users_tenant_id_set;
ALTER TABLE users DROP COLUMN tenant_id;
