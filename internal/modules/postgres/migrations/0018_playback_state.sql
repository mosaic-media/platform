-- Migration 0018 — Playback state.
--
-- Where each viewer got to (ADR 0046). This is the first per-user row in the
-- content domain: nodes, parts, relations and source bindings are all
-- install-global, shared by everyone using one Mosaic, and a position is the one
-- thing that is genuinely personal.
--
-- **Keyed by (user, node), and not by part.** A viewer resumes an episode, not
-- the 1080p Torrentio release of an episode. Someone who watches half from one
-- source and finishes from another has one position; keying by part would
-- present that as two half-watched copies of the same episode. Which release
-- served the bytes is recorded beside the position rather than inside the key.
--
-- **Not in node attributes**, which is where it would have been cheapest to put
-- it. Attributes are per-node and shared; a per-user dimension encoded inside a
-- shared document is a concurrency hazard and a privacy leak between the users
-- of one install.

CREATE TABLE IF NOT EXISTS playback_states (
    user_id   text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id   uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,

    -- The release last played, so a resume returns to the same encode: two
    -- encodes of one film differ by however much their intros differ, and
    -- resuming a different one lands the viewer at the wrong moment. A hint
    -- rather than a requirement — it is nullable because a release can be
    -- deleted while the position it produced remains valid, which is the whole
    -- point of keying on the node.
    part_id   uuid REFERENCES parts (id) ON DELETE SET NULL,

    -- Milliseconds rather than an interval or a float. An interval is more
    -- expressive than a position needs and reads back through a driver
    -- awkwardly; a float invites drift on a value that is compared against a
    -- threshold. Milliseconds are exact, and are what every player reports.
    position_ms bigint NOT NULL DEFAULT 0 CHECK (position_ms >= 0),
    -- The item's length as the *player* reported it, 0 when unknown. From the
    -- client rather than from the Part, because a Part's duration is frequently
    -- absent and the player always knows.
    duration_ms bigint NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),

    finished          boolean NOT NULL DEFAULT false,
    -- Whether a person said so, rather than a threshold deciding. Deriving
    -- alone gets credits wrong in both directions; manual alone is a chore
    -- nobody does. So a threshold decides by default, a person overrides, and
    -- their answer is never re-derived away — which needs this column to be
    -- distinguishable from the derived case.
    finished_explicit boolean NOT NULL DEFAULT false,

    updated_at timestamptz NOT NULL,

    PRIMARY KEY (user_id, node_id)
);

-- ON DELETE CASCADE on both parents. A position is not a record of anything
-- that must outlive its subject: removing the user or the item should take it,
-- and neither removal should be blocked by it. That is the opposite of the
-- content model's own stance (ADR 0013 makes content deletion explicit and
-- restricted), and deliberately so — this is state about watching, not content.

-- The continue-watching query: one user's unfinished items, most recent first.
-- Partial on `NOT finished` because that is the only rail this serves, and a
-- finished item keeps its position — so an index over everything would grow
-- with a user's whole watch history to answer a question about the last few
-- things they started.
CREATE INDEX IF NOT EXISTS playback_states_in_progress_idx
    ON playback_states (user_id, updated_at DESC)
    WHERE NOT finished;
