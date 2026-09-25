-- Phase C step 5b: the capacity module. An agent's vehicle has a capacity
-- (weight and volume) and capabilities (cold_chain, tail_lift, ...); a job
-- has a size and may require capabilities. NULL capacity means "not
-- declared": with the capacity module on, such an agent gets no sized
-- jobs, because nobody can say the load fits.

-- +goose Up
ALTER TABLE agents ADD COLUMN capacity_g BIGINT CHECK (capacity_g > 0);
ALTER TABLE agents ADD COLUMN capacity_cm3 BIGINT CHECK (capacity_cm3 > 0);
ALTER TABLE agents ADD COLUMN capabilities TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE pending_assignments ADD COLUMN weight_g BIGINT NOT NULL DEFAULT 0;
ALTER TABLE pending_assignments ADD COLUMN volume_cm3 BIGINT NOT NULL DEFAULT 0;
ALTER TABLE pending_assignments ADD COLUMN requires TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE pending_assignments DROP COLUMN requires;
ALTER TABLE pending_assignments DROP COLUMN volume_cm3;
ALTER TABLE pending_assignments DROP COLUMN weight_g;
ALTER TABLE agents DROP COLUMN capabilities;
ALTER TABLE agents DROP COLUMN capacity_cm3;
ALTER TABLE agents DROP COLUMN capacity_g;
