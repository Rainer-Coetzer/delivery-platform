-- Phase C step 3: the generic job (ADR-0003). A job belongs to a pack,
-- remembers the last milestone it reached, keeps its lifecycle facts in one
-- JSON object instead of a column per fact, and has an ordered list of
-- stops instead of fixed pickup/dropoff columns. Existing rows are food
-- jobs; every value below is derived from what they already hold.

-- +goose Up
ALTER TABLE jobs ADD COLUMN pack TEXT NOT NULL DEFAULT 'food';
ALTER TABLE jobs ALTER COLUMN pack DROP DEFAULT;

ALTER TABLE jobs ADD COLUMN milestone TEXT;
UPDATE jobs SET milestone = CASE status
    WHEN 'CREATED'          THEN 'created'
    WHEN 'CONFIRMED'        THEN 'confirmed'
    WHEN 'PREPARING'        THEN 'confirmed' -- no milestone of its own: the last one reached
    WHEN 'READY'            THEN 'ready_for_dispatch'
    WHEN 'OUT_FOR_DELIVERY' THEN 'in_transit'
    WHEN 'DELIVERED'        THEN 'completed'
    WHEN 'CANCELLED'        THEN 'cancelled'
END;
-- Fails the migration, loudly, if some row had a status food never had.
ALTER TABLE jobs ALTER COLUMN milestone SET NOT NULL;
CREATE INDEX jobs_open_idx ON jobs (merchant_id) WHERE milestone NOT IN ('completed', 'cancelled');

ALTER TABLE jobs ADD COLUMN facts JSONB NOT NULL DEFAULT '{}';
UPDATE jobs SET facts = jsonb_build_object('paid', paid, 'merchant_accepted', merchant_accepted);
ALTER TABLE jobs DROP COLUMN paid;
ALTER TABLE jobs DROP COLUMN merchant_accepted;

-- A courier job has no merchant.
ALTER TABLE jobs ALTER COLUMN merchant_id DROP NOT NULL;
ALTER TABLE jobs ALTER COLUMN merchant_name DROP NOT NULL;

CREATE TABLE job_stops (
    job_id       TEXT NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    seq          INT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('pickup', 'dropoff')),
    lat          DOUBLE PRECISION NOT NULL,
    lng          DOUBLE PRECISION NOT NULL,
    label        TEXT,
    merchant_id  TEXT,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (job_id, seq)
);

-- Food: the pickup is done once the job left READY, the dropoff once it
-- was delivered. The job's updated_at is the best time on record.
INSERT INTO job_stops (job_id, seq, kind, lat, lng, label, merchant_id, completed_at)
SELECT id, 0, 'pickup', pickup_lat, pickup_lng, pickup_label, merchant_id,
       CASE WHEN status IN ('OUT_FOR_DELIVERY', 'DELIVERED') THEN updated_at END
FROM jobs;
INSERT INTO job_stops (job_id, seq, kind, lat, lng, label, completed_at)
SELECT id, 1, 'dropoff', dropoff_lat, dropoff_lng, dropoff_label,
       CASE WHEN status = 'DELIVERED' THEN updated_at END
FROM jobs;

ALTER TABLE jobs DROP COLUMN pickup_lat;
ALTER TABLE jobs DROP COLUMN pickup_lng;
ALTER TABLE jobs DROP COLUMN pickup_label;
ALTER TABLE jobs DROP COLUMN dropoff_lat;
ALTER TABLE jobs DROP COLUMN dropoff_lng;
ALTER TABLE jobs DROP COLUMN dropoff_label;

-- +goose Down
ALTER TABLE jobs ADD COLUMN pickup_lat DOUBLE PRECISION;
ALTER TABLE jobs ADD COLUMN pickup_lng DOUBLE PRECISION;
ALTER TABLE jobs ADD COLUMN pickup_label TEXT;
ALTER TABLE jobs ADD COLUMN dropoff_lat DOUBLE PRECISION;
ALTER TABLE jobs ADD COLUMN dropoff_lng DOUBLE PRECISION;
ALTER TABLE jobs ADD COLUMN dropoff_label TEXT;
UPDATE jobs j SET pickup_lat = s.lat, pickup_lng = s.lng, pickup_label = s.label
FROM job_stops s WHERE s.job_id = j.id AND s.seq = 0;
UPDATE jobs j SET dropoff_lat = s.lat, dropoff_lng = s.lng, dropoff_label = s.label
FROM job_stops s WHERE s.job_id = j.id AND s.seq = (SELECT max(seq) FROM job_stops WHERE job_id = j.id);
ALTER TABLE jobs ALTER COLUMN pickup_lat SET NOT NULL;
ALTER TABLE jobs ALTER COLUMN pickup_lng SET NOT NULL;
ALTER TABLE jobs ALTER COLUMN dropoff_lat SET NOT NULL;
ALTER TABLE jobs ALTER COLUMN dropoff_lng SET NOT NULL;
DROP TABLE job_stops;

ALTER TABLE jobs ALTER COLUMN merchant_name SET NOT NULL;
ALTER TABLE jobs ALTER COLUMN merchant_id SET NOT NULL;

ALTER TABLE jobs ADD COLUMN paid BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE jobs ADD COLUMN merchant_accepted BOOLEAN NOT NULL DEFAULT false;
UPDATE jobs SET paid = coalesce((facts ->> 'paid')::boolean, false),
                merchant_accepted = coalesce((facts ->> 'merchant_accepted')::boolean, false);
ALTER TABLE jobs DROP COLUMN facts;

DROP INDEX jobs_open_idx;
ALTER TABLE jobs DROP COLUMN milestone;
ALTER TABLE jobs DROP COLUMN pack;
