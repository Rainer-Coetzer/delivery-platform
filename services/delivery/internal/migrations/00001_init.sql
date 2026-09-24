-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE processed_events (
    event_id   TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE outbox (
    id            BIGSERIAL PRIMARY KEY,
    event_id      TEXT NOT NULL UNIQUE,
    event_type    TEXT NOT NULL,
    order_id      TEXT,
    partition_key TEXT NOT NULL,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    sent_at       TIMESTAMPTZ
);
CREATE INDEX outbox_unsent_idx ON outbox (id) WHERE sent_at IS NULL;

CREATE TABLE drivers (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    vehicle    TEXT NOT NULL DEFAULT 'bike',
    status     TEXT NOT NULL DEFAULT 'OFFLINE', -- OFFLINE, AVAILABLE, BUSY
    lat        DOUBLE PRECISION NOT NULL,
    lng        DOUBLE PRECISION NOT NULL,
    location   GEOGRAPHY(Point, 4326) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- GiST index over the geography column is what makes the nearest-driver
-- <-> query in store.FindNearestAvailable fast instead of a full scan.
CREATE INDEX drivers_location_gix ON drivers USING GIST (location);
CREATE INDEX drivers_status_idx ON drivers (status);

CREATE TABLE deliveries (
    id              TEXT PRIMARY KEY,
    order_id        TEXT NOT NULL UNIQUE,
    driver_id       TEXT REFERENCES drivers (id),
    restaurant_id   TEXT NOT NULL,
    pickup_lat      DOUBLE PRECISION NOT NULL,
    pickup_lng      DOUBLE PRECISION NOT NULL,
    dropoff_lat     DOUBLE PRECISION NOT NULL,
    dropoff_lng     DOUBLE PRECISION NOT NULL,
    status          TEXT NOT NULL DEFAULT 'ASSIGNED', -- ASSIGNED, PICKED_UP, COMPLETED
    simulated       BOOLEAN NOT NULL DEFAULT true,
    eta_minutes     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX deliveries_driver_idx ON deliveries (driver_id);
CREATE INDEX deliveries_status_idx ON deliveries (status);

-- An order that reached READY while no driver was free. Retried on a timer
-- until a driver becomes available, instead of dropping the order.
CREATE TABLE pending_assignments (
    order_id      TEXT PRIMARY KEY,
    restaurant_id TEXT NOT NULL,
    pickup_lat    DOUBLE PRECISION NOT NULL,
    pickup_lng    DOUBLE PRECISION NOT NULL,
    dropoff_lat   DOUBLE PRECISION NOT NULL,
    dropoff_lng   DOUBLE PRECISION NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE pending_assignments;
DROP TABLE deliveries;
DROP TABLE drivers;
DROP TABLE outbox;
DROP TABLE processed_events;
