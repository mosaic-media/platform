-- Migration 0019 — Artwork stored on the node.
--
-- The descriptive surface is otherwise re-derived live from the provider on
-- every read (sdk#3), which is right for a detail screen — one item, one
-- fetch, always current. Artwork is the exception (platform#45): it is rendered in
-- bulk on list surfaces like the continue-watching rail, where re-deriving is a
-- provider round-trip per card, and it is the one image a user may later want to
-- override, which is possible only for something the library owns.
--
-- A jsonb document rather than three text columns: it matches external_ids and
-- attributes beside it, holds the provider's primary URL per type today, and
-- leaves room for a candidate set and a user selection later without a second
-- migration.
--
-- NOT NULL DEFAULT '{}': a node written before this column existed, or by a
-- source that had no art, reads as an empty document — "fall back", not a NULL
-- to special-case at every read.

ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS artwork jsonb NOT NULL DEFAULT '{}'::jsonb;
