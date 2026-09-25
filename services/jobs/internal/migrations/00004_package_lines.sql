-- Phase C step 5b: the packages module. A job line is either a catalogue
-- item (as before) or a package the customer declares: weight, dimensions
-- and declared value, no catalogue item. Existing lines are catalogue lines.

-- +goose Up
ALTER TABLE job_lines ADD COLUMN kind TEXT NOT NULL DEFAULT 'catalogue' CHECK (kind IN ('catalogue', 'package'));
ALTER TABLE job_lines ALTER COLUMN kind DROP DEFAULT;
ALTER TABLE job_lines ALTER COLUMN catalogue_item_id DROP NOT NULL;
ALTER TABLE job_lines ADD COLUMN weight_g BIGINT;
ALTER TABLE job_lines ADD COLUMN length_mm INT;
ALTER TABLE job_lines ADD COLUMN width_mm INT;
ALTER TABLE job_lines ADD COLUMN height_mm INT;
ALTER TABLE job_lines ADD COLUMN declared_value_cents BIGINT;
-- The shape of a line follows its kind, whatever the API lets through.
ALTER TABLE job_lines ADD CONSTRAINT job_lines_kind_shape CHECK (
    (kind = 'catalogue' AND catalogue_item_id IS NOT NULL)
    OR (kind = 'package' AND weight_g > 0 AND length_mm > 0 AND width_mm > 0 AND height_mm > 0)
);

-- +goose Down
DELETE FROM job_lines WHERE kind = 'package';
ALTER TABLE job_lines DROP CONSTRAINT job_lines_kind_shape;
ALTER TABLE job_lines DROP COLUMN declared_value_cents;
ALTER TABLE job_lines DROP COLUMN height_mm;
ALTER TABLE job_lines DROP COLUMN width_mm;
ALTER TABLE job_lines DROP COLUMN length_mm;
ALTER TABLE job_lines DROP COLUMN weight_g;
ALTER TABLE job_lines ALTER COLUMN catalogue_item_id SET NOT NULL;
ALTER TABLE job_lines DROP COLUMN kind;
