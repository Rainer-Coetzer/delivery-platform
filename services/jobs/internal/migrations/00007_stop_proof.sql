-- Phase C step 5e: the proof module. A stop records what proof it needs,
-- the PIN for a PIN stop (shown to the customer only; dispatch gets a
-- hash), and a pointer to the proof dispatch captured.

-- +goose Up
ALTER TABLE job_stops ADD COLUMN proof TEXT;
ALTER TABLE job_stops ADD COLUMN pin TEXT;
ALTER TABLE job_stops ADD COLUMN pin_hash TEXT;
ALTER TABLE job_stops ADD COLUMN captured_proof TEXT;
ALTER TABLE job_stops ADD COLUMN captured_by_assignment TEXT;

-- +goose Down
ALTER TABLE job_stops DROP COLUMN captured_by_assignment;
ALTER TABLE job_stops DROP COLUMN captured_proof;
ALTER TABLE job_stops DROP COLUMN pin_hash;
ALTER TABLE job_stops DROP COLUMN pin;
ALTER TABLE job_stops DROP COLUMN proof;
