-- Runs once (as the postgres superuser), when the Postgres container's data directory is empty (the
-- standard docker-entrypoint-initdb.d behaviour). Creates one role and one
-- database per service, so every service is isolated even though they
-- share a single Postgres instance in this development setup.
--
-- Plain statements on purpose: CREATE DATABASE can't run inside a DO block
-- or function, which is how the first version of this file broke first boot.
--
-- Roles are ordinary logins that own only their own database (audit P0-7:
-- they used to be SUPERUSER). The one thing that needed superuser, PostGIS,
-- is created here instead, as the postgres superuser, before any service
-- migrates. The dispatch migration's CREATE EXTENSION IF NOT EXISTS then
-- finds it and needs no privilege.

-- lite profile: one database for the whole platform binary, one schema
-- per module inside it (ADR-0002). The platform role owns the database, so
-- it can create those schemas itself at boot.
CREATE ROLE platform     LOGIN PASSWORD 'platform';
CREATE DATABASE platform OWNER platform;

-- full profile: one role and database per microservice.
-- Renamed in Phase C (customer -> identity, restaurant -> merchant, order ->
-- jobs, delivery -> dispatch, admin -> reporting). This file only runs on an
-- empty data directory; an existing `full` install keeps its old database
-- names, which is fine: DATABASE_URL names the database, and the tables
-- inside are renamed by each service's own migrations.
CREATE ROLE identity     LOGIN PASSWORD 'identity';
CREATE ROLE merchant     LOGIN PASSWORD 'merchant';
CREATE ROLE jobs         LOGIN PASSWORD 'jobs';
CREATE ROLE payment      LOGIN PASSWORD 'payment';
CREATE ROLE dispatch     LOGIN PASSWORD 'dispatch';
CREATE ROLE notification LOGIN PASSWORD 'notification';
CREATE ROLE reporting    LOGIN PASSWORD 'reporting';

CREATE DATABASE identity_db     OWNER identity;
CREATE DATABASE merchant_db     OWNER merchant;
CREATE DATABASE jobs_db         OWNER jobs;
CREATE DATABASE payment_db      OWNER payment;
CREATE DATABASE dispatch_db     OWNER dispatch;
CREATE DATABASE notification_db OWNER notification;
CREATE DATABASE reporting_db    OWNER reporting;

\connect dispatch_db
CREATE EXTENSION IF NOT EXISTS postgis;

-- In the platform database PostGIS lives in public; every module's
-- search_path is "<module>, public" (pkg/db.OpenSchema), so the dispatch
-- module still finds the geography type.
\connect platform
CREATE EXTENSION IF NOT EXISTS postgis;
