-- Phase C step 5g: open_offer dispatch. A waiting job is offered to a few
-- agents at once; the first to accept gets it. Offers hang off the queued
-- job, so cancelling (which deletes the queue row) withdraws them too.

-- +goose Up
ALTER TABLE pending_assignments ADD COLUMN offered_until TIMESTAMPTZ;

CREATE TABLE assignment_offers (
    job_id     TEXT NOT NULL REFERENCES pending_assignments (job_id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL REFERENCES agents (id),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, agent_id)
);
CREATE INDEX assignment_offers_agent_idx ON assignment_offers (agent_id, expires_at);

-- +goose Down
DROP TABLE assignment_offers;
ALTER TABLE pending_assignments DROP COLUMN offered_until;
