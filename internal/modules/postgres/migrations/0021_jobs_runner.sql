-- Migration 0021 — the jobs runner.
--
-- Migration 0006 created jobs, job_attempts and job_logs and said plainly that
-- there was no runner. This is the runner's half: the columns a claim-and-lease
-- loop needs, and nothing that only a scheduler with features nobody has asked
-- for would want.
--
-- `scheduled_at` already existed and nothing ever wrote it. It becomes the
-- claimable-after time — the one field a retry backoff moves — rather than a
-- second column meaning almost the same thing.

ALTER TABLE jobs ADD COLUMN IF NOT EXISTS attempt      integer     NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS max_attempts integer     NOT NULL DEFAULT 5;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS leased_by    text        NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS leased_until timestamptz;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS last_error   text        NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS updated_at   timestamptz;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS finished_at  timestamptz;

-- The idempotency key a recurring schedule enqueues under: one row per (kind,
-- occurrence). It is what makes "enqueue this hour's sweep" safe to call from
-- every process on every tick and at every boot — the second caller conflicts
-- and does nothing, rather than queueing a duplicate.
--
-- Partial, because a job enqueued by hand carries no key and several such rows
-- must be allowed to coexist. NULLs are distinct in a plain unique index too,
-- but a partial index says which rows the constraint is about.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS schedule_key text;
CREATE UNIQUE INDEX IF NOT EXISTS jobs_schedule_key_idx
    ON jobs (schedule_key) WHERE schedule_key IS NOT NULL;

-- The claim predicate's index. A runner asks for pending work that is due, and
-- for running work whose lease has expired, in one statement; both halves read
-- status first.
CREATE INDEX IF NOT EXISTS jobs_claim_idx ON jobs (status, scheduled_at);
CREATE INDEX IF NOT EXISTS jobs_lease_idx ON jobs (status, leased_until);

-- Attempts record how long each try took and why it ended. `duration_us`
-- rather than a computed difference so a row that lost its finished_at to a
-- crash is still legible as "started and never finished".
ALTER TABLE job_attempts ADD COLUMN IF NOT EXISTS duration_us bigint NOT NULL DEFAULT 0;
ALTER TABLE job_attempts ADD COLUMN IF NOT EXISTS runner      text   NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS job_attempts_job_id_idx ON job_attempts (job_id);
CREATE INDEX IF NOT EXISTS job_logs_logged_at_idx  ON job_logs (job_id, logged_at);

-- The trace the line was written inside. A job run mints its own root trace —
-- it is nobody's request — and carrying the id here is what joins a job that
-- failed a fortnight ago to the waterfall of the attempt that failed, for as
-- long as telemetry retention still holds it. Empty for a line written outside
-- a trace, which is honest rather than a run of zeros.
ALTER TABLE job_logs ADD COLUMN IF NOT EXISTS trace text NOT NULL DEFAULT '';
