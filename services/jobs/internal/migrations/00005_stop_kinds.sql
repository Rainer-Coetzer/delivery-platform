-- Phase C step 5c: the multi-stop module adds waypoint, service and return
-- stops (pkg/packs.StopKinds). 00003 allowed only pickup and dropoff.

-- +goose Up
ALTER TABLE job_stops DROP CONSTRAINT job_stops_kind_check;
ALTER TABLE job_stops ADD CONSTRAINT job_stops_kind_check
    CHECK (kind IN ('pickup', 'dropoff', 'waypoint', 'service', 'return'));

-- +goose Down
DELETE FROM job_stops WHERE kind NOT IN ('pickup', 'dropoff');
ALTER TABLE job_stops DROP CONSTRAINT job_stops_kind_check;
ALTER TABLE job_stops ADD CONSTRAINT job_stops_kind_check CHECK (kind IN ('pickup', 'dropoff'));
