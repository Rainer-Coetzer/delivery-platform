-- Phase C step 5d: the scheduling module. A stop may have a booked time
-- window (UTC instants); none means as soon as possible.

-- +goose Up
ALTER TABLE job_stops ADD COLUMN window_start TIMESTAMPTZ;
ALTER TABLE job_stops ADD COLUMN window_end TIMESTAMPTZ;
ALTER TABLE job_stops ADD CONSTRAINT job_stops_window_shape
    CHECK ((window_start IS NULL) = (window_end IS NULL) AND (window_end IS NULL OR window_end > window_start));

-- +goose Down
ALTER TABLE job_stops DROP CONSTRAINT job_stops_window_shape;
ALTER TABLE job_stops DROP COLUMN window_end;
ALTER TABLE job_stops DROP COLUMN window_start;
