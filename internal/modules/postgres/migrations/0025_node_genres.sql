-- Migration 0025 — Genres stored on the node.
--
-- The second field to cross from "re-derived live from the provider" (sdk#3)
-- to "stored on the node", after artwork (platform#45), and it crosses for a
-- related but distinct reason.
--
-- Artwork moved because it is **rendered** in bulk: a rail of in-progress items
-- re-deriving art is one provider round trip per card. Genre moves because it is
-- **filtered** in bulk — a facet is one question asked across the whole library
-- at once, and there is no number of round trips that answers it.
--
-- The alternative was the neighbouring `attributes` document, and it is rejected
-- for platform#45's reason: attributes are per-media-type variation the schema does
-- not validate and a module owns (platform#9), while a genre is universal and the
-- Platform is the thing filtering on it. There is also a sharper reason here
-- than there was for artwork. A facet has to be *complete*: a genre chip that
-- silently omits half the library because a module spelled its key differently
-- is an omission from a filtered list, and unlike a missing poster nobody can
-- see it.
--
-- `text[]` rather than jsonb, unlike artwork beside it. Artwork is a document
-- with named slots that will grow a candidate set (platform#47); this is a set of
-- strings, and the question asked of it is "does this row hold this value",
-- which `&&` answers off a GIN index over the array directly. Wrapping a string
-- array in JSON to ask a set question of it would be storing the shape wrong to
-- match a neighbour.
--
-- NOT NULL DEFAULT '{}': a node written before this column existed, or by a
-- source that named no genres, reads as the empty set. Those are the same fact
-- — "no genres are known" — and neither is a NULL to special-case at every read.

ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS genres text[] NOT NULL DEFAULT '{}';

-- The index the facet reads. `&&` (overlaps) and `@>` (contains) both ride
-- gin__int_ops-style array indexing; the default `array_ops` covers both.
--
-- Partial on works, because that is the only kind of node that carries genres:
-- a season and an episode belong to their work's genres and are never queried
-- for their own. In a library of 37,000 episode nodes under 800 works, indexing
-- the works alone is the difference between an index over the library and an
-- index over a rounding error of it.
CREATE INDEX IF NOT EXISTS nodes_genres_gin
    ON nodes USING gin (genres)
    WHERE node_kind = 'work';
