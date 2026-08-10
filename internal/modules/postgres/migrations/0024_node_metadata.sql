-- Migration 0024 — The stored metadata document (platform#62).
--
-- What a metadata provider said about a materialised title, kept so a library
-- detail renders from the object graph rather than from a live provider call.
-- Before this, a node-id detail had nothing to draw with: sdk#3 re-derived
-- everything from the provider on every render, keyed by a ref, and a library
-- card opens its node by id — so the two never met and the screen was blank.
--
-- **A table rather than a column on `nodes`** (platform#62). Artwork lives on the
-- node because it is read on every card of every list; this is read on one
-- screen at a time, and carrying it through every list read that never opens it
-- would put a document on the hot path to serve the cold one. It also has a
-- lifecycle the node does not — when it was fetched, and therefore how stale it
-- is — and that belongs to the cache.
--
-- **Platform-owned, unlike `nodes.attributes`.** Attributes are module-owned and
-- never interpreted (platform#9); this is read by the Platform to draw a screen.

CREATE TABLE IF NOT EXISTS node_metadata (
    node_id    uuid        PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    -- The whole descriptive document as one jsonb value: overview, year,
    -- runtime, genres, rating, certification, cast, crew, keywords, trailers,
    -- similar and collection.
    --
    -- One document rather than columns, because the Platform stores it and reads
    -- it back whole to render one screen — it never filters or sorts on a field
    -- inside it. The moment something does (faceting by genre is the candidate,
    -- M2b), that field earns a column and an index of its own, exactly as
    -- artwork earned one; guessing which now would be inventing a schema for a
    -- query nobody makes.
    document   jsonb       NOT NULL DEFAULT '{}',
    -- Which module answered, so a document can be attributed and a provider that
    -- was replaced does not leave its answers looking like the new one's.
    source     text        NOT NULL DEFAULT '',
    -- When it was fetched. The staleness question is asked of this, and it is
    -- what a future retention or refresh-ordering pass sorts by — the same shape
    -- the watch-provider records already carry a `checkedAt` for.
    fetched_at timestamptz NOT NULL
);

-- ON DELETE CASCADE, which is deliberate and is the one place in the content
-- model that cascades. Everywhere else deletion is RESTRICT because platform#9
-- rules that a deletion is a decision a user confirms and never a silent
-- cascade — but that protects *content*, and this is a cache of something a
-- provider said. A document surviving the node it describes is a row nothing can
-- ever read, keyed to an id that will never exist again.

-- Ordered staleness, for the refresh that will want "the oldest documents
-- first". Nothing sorts by it yet; the index is cheap and the alternative is
-- discovering it is missing during the pass that needs it.
CREATE INDEX IF NOT EXISTS node_metadata_fetched_at_idx
    ON node_metadata (fetched_at);
