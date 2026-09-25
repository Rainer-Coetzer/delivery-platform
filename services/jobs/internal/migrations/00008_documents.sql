-- Phase C step 5f: the documents module. Files on a job (waybills,
-- permits, customs forms); the bytes live in the blob store.

-- +goose Up
CREATE TABLE job_documents (
    id            TEXT PRIMARY KEY,
    job_id        TEXT NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    size_bytes    BIGINT NOT NULL CHECK (size_bytes > 0),
    blob_key      TEXT NOT NULL,
    uploaded_by   TEXT NOT NULL,
    uploaded_role TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX job_documents_job_idx ON job_documents (job_id, created_at);

-- +goose Down
DROP TABLE job_documents;
