-- Migration 0027 — The durable snapshot a source-backed screen renders from
-- (platform#30).
--
-- Restarting the Platform under a live client produced a home screen reading
-- "Nothing to show yet — try adding an addon in Settings" on an install with an
-- addon configured and a library full of content. The emit-side fans out to
-- every catalog and keeps what succeeds; when every cold call failed, no row
-- survived and the empty state rendered. A transient upstream failure was
-- therefore presented as a configuration mistake, which is worse than an error:
-- it sends a user to fix something that is not broken.
--
-- **Items, never a rendered tree.** Caching the `UINode` tree would be faster
-- and would break invisibly: artwork URLs are signed with a process-scoped key
-- and playback tickets sealed with another, both regenerated on boot, so a tree
-- cached before a restart comes back full of URLs signed by a key that no longer
-- exists — the images fail and the page looks right.
--
-- **Durable rather than in-memory**, because the point is surviving the restart
-- an in-memory cache evaporates on, and because a source being down for an hour
-- is the more common case than a reboot.

CREATE TABLE IF NOT EXISTS source_snapshots (
    -- Which module answered. Keyed by it so a provider that has been replaced
    -- cannot have its answers read back as the new one's, and so a failing
    -- source can be named in what the screen says.
    source     text        NOT NULL,
    -- What was asked, in the asker's own spelling: a catalog's native type and
    -- id for a page of items, the empty string for "what catalogs do you have".
    -- Opaque to storage — this table keeps answers and does not interpret the
    -- questions.
    key        text        NOT NULL,
    -- The answer as one jsonb value, marshalled from the SDK's own types. One
    -- document rather than columns for the reason node_metadata's is: the
    -- Platform reads it back whole to render, and never filters or sorts on a
    -- field inside it.
    document   jsonb       NOT NULL DEFAULT '[]',
    -- When the source gave this answer. A screen served from a snapshot has to
    -- be able to say how old it is; a stale screen presented as live is the
    -- failure this whole table exists to avoid repeating in a quieter form.
    taken_at   timestamptz NOT NULL,
    PRIMARY KEY (source, key)
);
