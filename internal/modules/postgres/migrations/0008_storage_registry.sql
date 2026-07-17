-- Migration 0008 — Storage registry (MEG-015 §05, First Schema Areas:
-- Storage registry). Tables: object records, logical ownership, retention
-- metadata. Table only this slice; the object registry service lands later.

CREATE TABLE IF NOT EXISTS object_records (
    id         text        PRIMARY KEY,
    kind       text        NOT NULL,
    location   text        NOT NULL,
    size_bytes bigint      NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS logical_ownership (
    object_id       text PRIMARY KEY REFERENCES object_records (id) ON DELETE CASCADE,
    owner_capability text NOT NULL
);

CREATE INDEX IF NOT EXISTS logical_ownership_owner_idx
    ON logical_ownership (owner_capability);

CREATE TABLE IF NOT EXISTS retention_metadata (
    object_id    text PRIMARY KEY REFERENCES object_records (id) ON DELETE CASCADE,
    retain_until timestamptz,
    policy       text NOT NULL DEFAULT ''
);
