-- Phase C step 3: dispatch now gets a job's stops from the jobs module (on
-- the step into ready_for_dispatch), so the merchant's copy of the pickup
-- and dropoff points has no reader left.

-- +goose Up
ALTER TABLE merchant_jobs DROP COLUMN pickup_lat;
ALTER TABLE merchant_jobs DROP COLUMN pickup_lng;
ALTER TABLE merchant_jobs DROP COLUMN dropoff_lat;
ALTER TABLE merchant_jobs DROP COLUMN dropoff_lng;

-- +goose Down
ALTER TABLE merchant_jobs ADD COLUMN pickup_lat DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE merchant_jobs ADD COLUMN pickup_lng DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE merchant_jobs ADD COLUMN dropoff_lat DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE merchant_jobs ADD COLUMN dropoff_lng DOUBLE PRECISION NOT NULL DEFAULT 0;
