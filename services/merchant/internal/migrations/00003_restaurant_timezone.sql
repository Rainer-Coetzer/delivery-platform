-- +goose Up

-- IANA zone the restaurant's opening hours are written in (audit P0-9:
-- hours were compared against UTC, so a 09:00-22:00 place in Windhoek
-- accepted orders 11:00-00:00 local). New restaurants must supply it; rows
-- that predate this migration get UTC, which is what they were implicitly
-- evaluated in before, so their behaviour doesn't change silently.
ALTER TABLE restaurants ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';

-- +goose Down
ALTER TABLE restaurants DROP COLUMN timezone;
