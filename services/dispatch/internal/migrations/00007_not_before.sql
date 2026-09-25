-- Phase C step 5d: the scheduling module. A booked job waits in the queue
-- until its pickup window opens (less the pack's lead time), instead of
-- being assigned the moment it is ready.

-- +goose Up
ALTER TABLE pending_assignments ADD COLUMN not_before TIMESTAMPTZ;

-- +goose Down
ALTER TABLE pending_assignments DROP COLUMN not_before;
