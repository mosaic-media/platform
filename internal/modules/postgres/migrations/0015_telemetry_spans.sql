-- Migration 0015 — Telemetry spans (platform#33, platform#36).
--
-- Logs say a thing happened; spans say how long it took and what it contained.
-- That difference is the whole reason this table exists: "the page took nine
-- seconds" is answerable from logs only by subtracting timestamps and guessing
-- what happened between them, whereas a span tree says the aggregator call took
-- eight of the nine.
--
-- Same shape as telemetry_logs on purpose — partitioned by day, BRIN on time,
-- dropped rather than deleted — so one maintenance path serves both and there is
-- one thing to understand rather than two. Spans are shorter-lived than logs
-- (platform#36: 72 hours against 14 days), which partitioning makes a matter of
-- when the DROP runs rather than a different mechanism.

CREATE TABLE IF NOT EXISTS telemetry_spans (
    time       timestamptz NOT NULL,
    trace      text        NOT NULL,
    span       text        NOT NULL,
    -- Empty for the root span of a trace. The waterfall is built by joining
    -- span to parent within one trace, which is why both are plain columns.
    parent     text        NOT NULL DEFAULT '',
    name       text        NOT NULL,
    component  text        NOT NULL DEFAULT '',
    module     text        NOT NULL DEFAULT '',
    service    text        NOT NULL DEFAULT '',
    instance   text        NOT NULL DEFAULT '',
    boot       text        NOT NULL DEFAULT '',
    -- Stored in microseconds rather than as an interval: it is compared and
    -- summed far more often than it is displayed, and a bigint does both
    -- without a cast.
    duration_us bigint     NOT NULL,
    -- 'ok' or 'error' — a closed vocabulary, so the viewer can colour by it and
    -- the store can index it without parsing prose.
    status     text        NOT NULL DEFAULT 'ok',
    -- One of the Platform's seven error categories when status is 'error'.
    -- Empty otherwise. The categories are a Platform contract, so this is safe
    -- verbatim in a way a raw error message would not be.
    error_category text    NOT NULL DEFAULT '',
    -- Already redacted at construction (platform#34), exactly as log fields are:
    -- a span attribute is not a second, laxer channel.
    attributes jsonb       NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (time);

CREATE INDEX IF NOT EXISTS telemetry_spans_time_brin
    ON telemetry_spans USING brin (time);

-- The waterfall lookup: every span of one trace, in order. This is the query
-- the expert-mode trace view is built on.
CREATE INDEX IF NOT EXISTS telemetry_spans_trace_idx
    ON telemetry_spans (trace, time);

-- "What is slow?" — the other question spans exist to answer.
CREATE INDEX IF NOT EXISTS telemetry_spans_name_duration_idx
    ON telemetry_spans (name, duration_us DESC);

-- Finding failures without scanning the healthy majority.
CREATE INDEX IF NOT EXISTS telemetry_spans_status_time_idx
    ON telemetry_spans (status, time DESC)
    WHERE status <> 'ok';
