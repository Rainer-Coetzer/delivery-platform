-- Phase C step 5e: the proof module. Each stop on a route carries the
-- proof it needs (and, for a PIN, the hash to check against, with a count
-- of wrong tries); stop_proofs is what the agent captured. One proof per
-- stop: a stop is done once.

-- +goose Up
ALTER TABLE assignment_stops ADD COLUMN proof TEXT;
ALTER TABLE assignment_stops ADD COLUMN pin_hash TEXT;
ALTER TABLE assignment_stops ADD COLUMN pin_attempts INT NOT NULL DEFAULT 0;

-- Who may look at the proof: the job's customer as well as the agent.
ALTER TABLE assignments ADD COLUMN customer_id TEXT;
ALTER TABLE pending_assignments ADD COLUMN customer_id TEXT;

CREATE TABLE stop_proofs (
    id            TEXT PRIMARY KEY,
    assignment_id TEXT NOT NULL,
    seq           INT NOT NULL,
    kind          TEXT NOT NULL, -- photo, signature, pin, id_check, admin_override, simulated
    blob_key      TEXT,          -- photo and signature only
    content_type  TEXT,
    size_bytes    BIGINT,
    details       JSONB NOT NULL DEFAULT '{}',
    submitted_by  TEXT NOT NULL, -- the user who captured it
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (assignment_id, seq) REFERENCES assignment_stops (assignment_id, seq) ON DELETE CASCADE,
    UNIQUE (assignment_id, seq)
);

-- +goose Down
DROP TABLE stop_proofs;
ALTER TABLE pending_assignments DROP COLUMN customer_id;
ALTER TABLE assignments DROP COLUMN customer_id;
ALTER TABLE assignment_stops DROP COLUMN pin_attempts;
ALTER TABLE assignment_stops DROP COLUMN pin_hash;
ALTER TABLE assignment_stops DROP COLUMN proof;
