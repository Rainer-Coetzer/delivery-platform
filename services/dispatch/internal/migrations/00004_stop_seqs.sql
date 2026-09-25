-- Phase C step 3: dispatch reports progress per job stop
-- (dispatch.stop-completed), so an assignment remembers which of the job's
-- stops its pickup and dropoff are. Every existing assignment is a two-stop
-- food job: pickup 0, dropoff 1. A courier pickup isn't at a merchant.

-- +goose Up
ALTER TABLE assignments ADD COLUMN pickup_seq INT NOT NULL DEFAULT 0;
ALTER TABLE assignments ADD COLUMN dropoff_seq INT NOT NULL DEFAULT 1;
ALTER TABLE assignments ALTER COLUMN pickup_seq DROP DEFAULT;
ALTER TABLE assignments ALTER COLUMN dropoff_seq DROP DEFAULT;
ALTER TABLE assignments ALTER COLUMN merchant_id DROP NOT NULL;

ALTER TABLE pending_assignments ADD COLUMN pickup_seq INT NOT NULL DEFAULT 0;
ALTER TABLE pending_assignments ADD COLUMN dropoff_seq INT NOT NULL DEFAULT 1;
ALTER TABLE pending_assignments ALTER COLUMN pickup_seq DROP DEFAULT;
ALTER TABLE pending_assignments ALTER COLUMN dropoff_seq DROP DEFAULT;
ALTER TABLE pending_assignments ALTER COLUMN merchant_id DROP NOT NULL;

-- +goose Down
UPDATE pending_assignments SET merchant_id = '' WHERE merchant_id IS NULL;
ALTER TABLE pending_assignments ALTER COLUMN merchant_id SET NOT NULL;
ALTER TABLE pending_assignments DROP COLUMN dropoff_seq;
ALTER TABLE pending_assignments DROP COLUMN pickup_seq;

UPDATE assignments SET merchant_id = '' WHERE merchant_id IS NULL;
ALTER TABLE assignments ALTER COLUMN merchant_id SET NOT NULL;
ALTER TABLE assignments DROP COLUMN dropoff_seq;
ALTER TABLE assignments DROP COLUMN pickup_seq;
