-- Migration 0009 — Event envelope and failure bookkeeping (MEG-015 §06).
--
-- Expands event_outbox with the full event envelope (recorded_at, actor,
-- tenant_scope, correlation_id, causation_id, redaction_class) and the
-- delivery failure-tracking columns (attempts, last_error_category,
-- next_retry_at, dead_lettered, owning_component).
--
-- This is a separate, additive migration rather than an edit to 0005 —
-- expand/contract (MEG-015 §05): editing an already-applied migration would
-- change its checksum and trip the incompatible-schema guard. Additive
-- columns carry safe defaults so the change applies cleanly to a table that
-- already holds rows.

-- Envelope fields.
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS recorded_at     timestamptz NOT NULL DEFAULT now();
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS actor           text        NOT NULL DEFAULT '';
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS tenant_scope    text        NOT NULL DEFAULT '';
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS correlation_id  text        NOT NULL DEFAULT '';
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS causation_id    text        NOT NULL DEFAULT '';
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS redaction_class text        NOT NULL DEFAULT 'sensitive';

-- Delivery failure bookkeeping (§06 — Failure Behaviour). The worker that
-- reads and updates these lands in a later slice.
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS attempts            integer     NOT NULL DEFAULT 0;
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS last_error_category text        NOT NULL DEFAULT '';
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS next_retry_at       timestamptz;
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS dead_lettered       boolean     NOT NULL DEFAULT false;
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS owning_component    text        NOT NULL DEFAULT '';

-- The outbox worker (later slice) polls unpublished, non-dead-lettered rows
-- whose retry is due. Replace the unpublished-only index with one that also
-- excludes dead-lettered rows to match that access pattern.
DROP INDEX IF EXISTS event_outbox_unpublished_idx;
CREATE INDEX IF NOT EXISTS event_outbox_deliverable_idx
    ON event_outbox (next_retry_at, occurred_at)
    WHERE published_at IS NULL AND dead_lettered = false;
