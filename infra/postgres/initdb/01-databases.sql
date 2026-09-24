-- Runs once (as the postgres superuser), when the Postgres container's data directory is empty (the
-- standard docker-entrypoint-initdb.d behaviour). Creates one role and one
-- database per service, so every service is isolated even though they
-- share a single Postgres instance in this development setup.
--
-- Plain statements on purpose: CREATE DATABASE can't run inside a DO block
-- or function, which is how the first version of this file broke first boot.
--
-- "order" is quoted because ORDER is a reserved word.
--
-- Roles are ordinary logins that own only their own database (audit P0-7:
-- they used to be SUPERUSER). The one thing that needed superuser, PostGIS,
-- is created here instead, as the postgres superuser, before any service
-- migrates. The delivery migration's CREATE EXTENSION IF NOT EXISTS then
-- finds it and needs no privilege.

CREATE ROLE customer     LOGIN PASSWORD 'customer';
CREATE ROLE restaurant   LOGIN PASSWORD 'restaurant';
CREATE ROLE "order"      LOGIN PASSWORD 'order';
CREATE ROLE payment      LOGIN PASSWORD 'payment';
CREATE ROLE delivery     LOGIN PASSWORD 'delivery';
CREATE ROLE notification LOGIN PASSWORD 'notification';
CREATE ROLE admin        LOGIN PASSWORD 'admin';

CREATE DATABASE customer_db     OWNER customer;
CREATE DATABASE restaurant_db   OWNER restaurant;
CREATE DATABASE order_db        OWNER "order";
CREATE DATABASE payment_db      OWNER payment;
CREATE DATABASE delivery_db     OWNER delivery;
CREATE DATABASE notification_db OWNER notification;
CREATE DATABASE admin_db        OWNER admin;

\connect delivery_db
CREATE EXTENSION IF NOT EXISTS postgis;
