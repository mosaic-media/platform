-- Migration 0026 — Where a title can be watched, as a queryable projection.
--
-- The half of the streaming-service facet that has been missing since the
-- capability landed. `module-tmdb` records availability in the node's
-- module-owned `attributes` document at import and the SDK's containment filter
-- reads it, which is enough to answer the question and not enough to answer it
-- *correctly*: nothing refreshed it, and availability churns monthly. A group
-- saying "on Netflix" about a title that left in March is worse than an absent
-- group, because a user can see a missing feature and cannot see a lying one.
-- So the surface was withheld on purpose.
--
-- Two things change here, and the second is what makes the first honest.
--
-- **The Platform keeps its own copy, from the value the SDK models.** A
-- provider's answer already carries `ContentMetadata.Watch` — a typed field, not
-- a module's dialect — and ADR 0107's enrichment pass already fetches it on
-- every refresh. Projecting *that* is not the Platform learning a module's
-- attribute key: it is the Platform storing a field the contract defines,
-- exactly as it already stores `Artwork` from the same answer. `tmdbWatch` stays
-- the module's own business and nothing here reads it.
--
-- **The row records when it was checked**, so the refresh has something to sort
-- by and a screen has something to be honest with. It is the stamp the module's
-- own records have carried all along for a refresh that did not exist.
--
-- A table rather than a column on `nodes`, for ADR 0107's reason: this has a
-- lifecycle the node does not — a region, a moment it was true — and a node
-- carrying it would put a fact with an expiry date on every list read that never
-- asks about it. Genre went on the node in 0025 because a genre does not expire.

CREATE TABLE IF NOT EXISTS node_watch_availability (
    node_id    uuid        PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,

    -- The region the answer is about. **Availability is national, and a
    -- substitute is a wrong answer rather than a partial one**, so this is
    -- stored beside the providers rather than assumed from configuration: a
    -- deployment that changes region must not read its old answers as its new
    -- ones.
    region     text        NOT NULL DEFAULT '',

    -- The service names, flat, because a set question is what is asked of them.
    -- The richer per-offer detail (subscription, rent, buy) stays in the
    -- document the metadata cache already holds; this is the projection a facet
    -- can be indexed on, and duplicating the whole shape here would be a second
    -- copy that drifts.
    providers  text[]      NOT NULL DEFAULT '{}',

    -- When the provider was last asked. The refresh orders by this — oldest
    -- first — so a bounded run eventually reaches every title and none starves,
    -- and a screen can say how old its answer is rather than implying it is now.
    checked_at timestamptz NOT NULL
);

-- The facet's index: "which works are on this service".
CREATE INDEX IF NOT EXISTS node_watch_availability_providers_gin
    ON node_watch_availability USING gin (providers);

-- The refresh's index: the stalest rows first.
CREATE INDEX IF NOT EXISTS node_watch_availability_checked_at_idx
    ON node_watch_availability (checked_at);
