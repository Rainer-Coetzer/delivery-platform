-- Runs once when the Postgres container's data directory is empty (the
-- standard docker-entrypoint-initdb.d behavior). Creates one role and one
-- database per service, so every service is isolated even though they
-- share a single Postgres instance in this development setup.
--
-- Roles are granted SUPERUSER only so the delivery service's own migration
-- can run `CREATE EXTENSION IF NOT EXISTS postgis` without a separate
-- privileged step. In a real deployment, extensions would be baked into
-- the base image and applied by an admin migration instead.

DO $$
DECLARE
    svc TEXT;
BEGIN
    FOREACH svc IN ARRAY ARRAY['customer', 'restaurant', 'order', 'payment', 'delivery', 'notification', 'admin']
    LOOP
        EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L SUPERUSER', svc, svc);
        EXECUTE format('CREATE DATABASE %I OWNER %I', svc || '_db', svc);
    END LOOP;
END $$;
