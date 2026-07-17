-- Migration 0005 — Events (MEG-015 §05, First Schema Areas: Events).
-- Tables: event outbox, event deliveries, event checkpoints.
-- This slice persists into event_outbox via the EventOutbox contract only.
-- The outbox worker, deliveries and checkpoints logic is a LATER slice
-- (MEG-015 §12 — Transactional outbox / Event Bus); their tables exist now.

CREATE TABLE IF NOT EXISTS event_outbox (
    id           text        PRIMARY KEY,
    type         text        NOT NULL,
    payload      bytea       NOT NULL,
    occurred_at  timestamptz NOT NULL,
    published_at timestamptz
);

-- Partial index over unpublished rows: the outbox worker (later slice) polls
-- exactly these.
CREATE INDEX IF NOT EXISTS event_outbox_unpublished_idx
    ON event_outbox (occurred_at)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS event_deliveries (
    id           text        PRIMARY KEY,
    event_id     text        NOT NULL REFERENCES event_outbox (id) ON DELETE CASCADE,
    subscriber   text        NOT NULL,
    delivered_at timestamptz,
    status       text        NOT NULL DEFAULT 'pending',
    UNIQUE (event_id, subscriber)
);

CREATE TABLE IF NOT EXISTS event_checkpoints (
    subscriber    text        PRIMARY KEY,
    last_event_id text,
    updated_at    timestamptz NOT NULL
);
