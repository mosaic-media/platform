-- Migration 0006 — Jobs (MEG-015 §05, First Schema Areas: Jobs).
-- Tables: jobs, job attempts, job logs. Table only this slice; the job
-- runner is not part of the Platform foundation build sequence yet.

CREATE TABLE IF NOT EXISTS jobs (
    id           text        PRIMARY KEY,
    kind         text        NOT NULL,
    payload      bytea       NOT NULL DEFAULT '\x',
    status       text        NOT NULL DEFAULT 'pending',
    created_at   timestamptz NOT NULL,
    scheduled_at timestamptz
);

CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs (status);

CREATE TABLE IF NOT EXISTS job_attempts (
    id          text        PRIMARY KEY,
    job_id      text        NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    attempt     integer     NOT NULL,
    started_at  timestamptz NOT NULL,
    finished_at timestamptz,
    status      text        NOT NULL DEFAULT 'running',
    error       text        NOT NULL DEFAULT '',
    UNIQUE (job_id, attempt)
);

CREATE TABLE IF NOT EXISTS job_logs (
    id        text        PRIMARY KEY,
    job_id    text        NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    logged_at timestamptz NOT NULL,
    level     text        NOT NULL DEFAULT 'info',
    message   text        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS job_logs_job_id_idx ON job_logs (job_id);
