-- +goose Up

-- One row per partition, holding the next sequence number. Publishers lock
-- this row for their whole transaction, so within a partition sequence
-- numbers are assigned in commit order. A plain BIGSERIAL would not do: ids
-- are handed out at insert time, and a transaction that took id 10 can
-- commit after one that took id 11. A dispatcher that has already read 11
-- would then skip 10 forever.
CREATE TABLE partition_heads (
    partition INT PRIMARY KEY,
    next_seq  BIGINT NOT NULL
);

CREATE TABLE log (
    partition  INT NOT NULL,
    seq        BIGINT NOT NULL,
    topic      TEXT NOT NULL,
    key        TEXT NOT NULL,
    payload    BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (partition, seq)
);

-- Per consumer group and partition: the last sequence number fully handled
-- (successfully or dead-lettered). Everything after it is redelivered after
-- a crash, hence at-least-once.
CREATE TABLE offsets (
    grp       TEXT NOT NULL,
    partition INT NOT NULL,
    last_seq  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (grp, partition)
);

CREATE TABLE dead_letters (
    id        BIGSERIAL PRIMARY KEY,
    grp       TEXT NOT NULL,
    topic     TEXT NOT NULL,
    key       TEXT NOT NULL,
    partition INT NOT NULL,
    seq       BIGINT NOT NULL,
    payload   BYTEA NOT NULL,
    reason    TEXT NOT NULL,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dead_letters_grp_idx ON dead_letters (grp, failed_at DESC);

-- +goose Down
DROP TABLE dead_letters;
DROP TABLE offsets;
DROP TABLE log;
DROP TABLE partition_heads;
