-- Migration 0014 — Telemetry storage (ADR 0058).
--
-- The queryable half of the dual sink. The durable half is a local .log file,
-- which is what survives a crash and what still records the fault when
-- PostgreSQL itself is the fault; this table exists so an administrator can
-- *ask questions* — filter by component, level or trace and get an answer —
-- which a flat file cannot serve.
--
-- Deliberately NOT reached through Tx. Every other Platform table commits
-- inside a UnitOfWork so state and its outbox event land atomically; telemetry
-- is the opposite in every respect. It is high volume, lossy by design, and it
-- must never fail or delay the request that produced it, so it is written by a
-- direct pooled batch writer outside any transaction. Audit is the deliberate
-- exception and goes in Tx precisely for the guarantees telemetry gives up
-- (ADR 0057).
--
-- Partitioned by day so retention is DROP TABLE rather than DELETE. On a
-- self-hosted box the difference matters more than it looks: deleting a day of
-- rows rewrites and vacuums a table an administrator is probably querying,
-- whereas dropping a partition is a catalogue update. Partitions are created
-- ahead of time by the Platform (internal/modules/postgres/telemetry_store.go)
-- rather than by a scheduler, because the jobs runner does not exist yet — that
-- is a stated gap in ADR 0058, not an oversight.

CREATE TABLE IF NOT EXISTS telemetry_logs (
    time      timestamptz NOT NULL,
    level     text        NOT NULL,
    service   text        NOT NULL DEFAULT '',
    instance  text        NOT NULL DEFAULT '',
    boot      text        NOT NULL DEFAULT '',
    -- trace and span are first-class columns, not keys inside fields: joining
    -- a log line to the event row and the span that produced it is the single
    -- most important query this table serves (ADR 0054), and it should not
    -- require reaching into jsonb to do it.
    trace     text        NOT NULL DEFAULT '',
    span      text        NOT NULL DEFAULT '',
    component text        NOT NULL DEFAULT '',
    module    text        NOT NULL DEFAULT '',
    message   text        NOT NULL,
    -- Already redacted at construction (ADR 0056). Nothing in here needs
    -- masking on read, which is what makes rendering these rows into an
    -- administrator's browser defensible at all.
    fields    jsonb       NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (time);

-- BRIN, not btree, on time. Rows arrive in time order and never move, so the
-- block-range summary is accurate, and it costs a few kilobytes where a btree
-- over every row would cost a substantial fraction of the table itself.
CREATE INDEX IF NOT EXISTS telemetry_logs_time_brin
    ON telemetry_logs USING brin (time);

-- The correlation lookup: "show me everything in this trace". Partial, because
-- records with no trace (boot narration, background work) are never fetched
-- this way and would otherwise dominate the index.
CREATE INDEX IF NOT EXISTS telemetry_logs_trace_idx
    ON telemetry_logs (trace)
    WHERE trace <> '';

-- The browse-and-filter path the expert-mode log viewer uses.
CREATE INDEX IF NOT EXISTS telemetry_logs_component_time_idx
    ON telemetry_logs (component, time DESC);

CREATE INDEX IF NOT EXISTS telemetry_logs_level_time_idx
    ON telemetry_logs (level, time DESC);
