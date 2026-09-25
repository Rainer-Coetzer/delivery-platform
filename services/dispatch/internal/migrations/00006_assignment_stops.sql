-- Phase C step 5c: the multi-stop module. An assignment visits every stop
-- of the job, in order, not just a pickup and a dropoff. assignment_stops
-- is that list; the pickup/dropoff columns on assignments stay as the
-- first pickup and the last stop, which is what the map and the ETA use.
-- A queued (pending) assignment keeps the whole route as JSON.

-- +goose Up
CREATE TABLE assignment_stops (
    assignment_id TEXT NOT NULL REFERENCES assignments (id) ON DELETE CASCADE,
    seq           INT NOT NULL,
    kind          TEXT NOT NULL,
    lat           DOUBLE PRECISION NOT NULL,
    lng           DOUBLE PRECISION NOT NULL,
    label         TEXT,
    completed_at  TIMESTAMPTZ,
    PRIMARY KEY (assignment_id, seq)
);

-- Existing assignments are two-stop food jobs; their progress is in status.
INSERT INTO assignment_stops (assignment_id, seq, kind, lat, lng, completed_at)
SELECT id, pickup_seq, 'pickup', pickup_lat, pickup_lng,
       CASE WHEN status IN ('PICKED_UP', 'COMPLETED') THEN coalesce(picked_up_at, updated_at) END
FROM assignments;
INSERT INTO assignment_stops (assignment_id, seq, kind, lat, lng, completed_at)
SELECT id, dropoff_seq, 'dropoff', dropoff_lat, dropoff_lng,
       CASE WHEN status = 'COMPLETED' THEN coalesce(completed_at, updated_at) END
FROM assignments;

ALTER TABLE pending_assignments ADD COLUMN stops JSONB NOT NULL DEFAULT '[]';
UPDATE pending_assignments SET stops = jsonb_build_array(
    jsonb_build_object('seq', pickup_seq, 'kind', 'pickup', 'merchantId', merchant_id,
                       'location', jsonb_build_object('lat', pickup_lat, 'lng', pickup_lng)),
    jsonb_build_object('seq', dropoff_seq, 'kind', 'dropoff',
                       'location', jsonb_build_object('lat', dropoff_lat, 'lng', dropoff_lng)));
ALTER TABLE pending_assignments ALTER COLUMN stops DROP DEFAULT;

-- +goose Down
ALTER TABLE pending_assignments DROP COLUMN stops;
DROP TABLE assignment_stops;
