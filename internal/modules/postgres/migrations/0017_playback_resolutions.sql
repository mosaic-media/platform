-- Migration 0017 — The playback resolution cache.
--
-- The perishable half of ADR 0049's durable/perishable split. Candidates are
-- Parts and never expire; where their bytes actually are does, on a schedule
-- nobody publishes — a debrid link dies either on the provider's own timetable
-- or the moment its torrent leaves the provider's cache, whichever comes first.
--
-- This table exists for one measurement. Ranking candidates is free, minting a
-- ticket is free, and asking the source is not: an aggregator that fans out to
-- many scrapers answers in hundreds of milliseconds to several seconds. Paying
-- that between a click and a first frame is the whole latency budget on one
-- call, every time, including the nine hundred and ninety-ninth time the answer
-- was the same.
--
-- Keyed by (part, capability class) and by nothing else. Not by user: the
-- library is install-global (ADR 0013) and a resolved URL is a property of the
-- bytes and the screen, so keying by person would store one identical row per
-- person for a value none of them owns. Not by device either: five identical
-- phones are one answer. What decides the answer is what the client can decode,
-- so that is the key.

CREATE TABLE IF NOT EXISTS playback_resolutions (
    part_id          uuid        NOT NULL REFERENCES parts (id) ON DELETE CASCADE,
    -- A stable digest of the client profile declared on Attach (ADR 0047).
    -- Opaque here on purpose: the Platform computes it and this table only
    -- groups by it, so changing how a class is derived is a code change and not
    -- a migration.
    capability_class text        NOT NULL,

    url              text        NOT NULL,
    -- Request headers the URL's origin requires, as JSON. Empty object when it
    -- can be fetched bare. They live beside the URL because they are exactly as
    -- perishable: a re-resolve replaces both or neither.
    headers          jsonb       NOT NULL DEFAULT '{}',
    -- When the source last answered. A diagnostic and a refresh hint, never a
    -- correctness input — ADR 0049 rejects a TTL as the mechanism precisely
    -- because an entry two minutes old can be dead and one two days old live.
    resolved_at      timestamptz NOT NULL,

    PRIMARY KEY (part_id, capability_class)
);

-- ON DELETE CASCADE, not RESTRICT: this is a cache, not a record of anything
-- that happened. Removing the candidate should take its addresses with it, and
-- an address should never be the reason a candidate cannot be deleted — which
-- is the opposite of the content model's stance (ADR 0013), and deliberately so.

-- "What has this class resolved lately" — the query the refresh job will ask
-- when it exists, and the one a lookup on the primary key cannot serve.
CREATE INDEX IF NOT EXISTS playback_resolutions_class_resolved_idx
    ON playback_resolutions (capability_class, resolved_at DESC);
