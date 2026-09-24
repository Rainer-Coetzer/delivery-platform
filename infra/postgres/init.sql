-- Runs once, when the Postgres container's data directory is empty (the
-- standard docker-entrypoint-initdb.d behaviour). Creates one role and one
-- database per service, so every service is isolated even though they
-- share a single Postgres instance in this development setup.
--
-- Plain statements on purpose: CREATE DATABASE can't run inside a DO block
-- or function, which is how the first version of this file broke first boot.
--
-- "order" is quoted because ORDER is a reserved word.
--
-- SUPERUSER exists only so the delivery service's own migration can run
-- CREATE EXTENSION postgis. Removing it (and creating PostGIS here instead)
-- is audit P0-7, scheduled for Phase 2.

CREATE ROLE customer     LOGIN PASSWORD 'customer'     SUPERUSER;
CREATE ROLE restaurant   LOGIN PASSWORD 'restaurant'   SUPERUSER;
CREATE ROLE "order"      LOGIN PASSWORD 'order'        SUPERUSER;
CREATE ROLE payment      LOGIN PASSWORD 'payment'      SUPERUSER;
CREATE ROLE delivery     LOGIN PASSWORD 'delivery'     SUPERUSER;
CREATE ROLE notification LOGIN PASSWORD 'notification' SUPERUSER;
CREATE ROLE admin        LOGIN PASSWORD 'admin'        SUPERUSER;

CREATE DATABASE customer_db     OWNER customer;
CREATE DATABASE restaurant_db   OWNER restaurant;
CREATE DATABASE order_db        OWNER "order";
CREATE DATABASE payment_db      OWNER payment;
CREATE DATABASE delivery_db     OWNER delivery;
CREATE DATABASE notification_db OWNER notification;
CREATE DATABASE admin_db        OWNER admin;
