-- Migration 0012 — The content model (platform#9, the object graph; platform#10,
-- storage authority). Tables: nodes, parts, relations, source_bindings.
--
-- This is the first schema in the database that is content rather than
-- infrastructure. Everything before it — identity, sessions, permissions,
-- configuration, events, jobs, diagnostics, the blob registry — is machinery;
-- these four tables are where an anime, a film or an album actually goes.
--
-- Identifiers here are UUIDv7 in native uuid columns. Time-ordered ids append
-- near the right-hand edge of a btree instead of scattering inserts across it,
-- and native uuid is 16 bytes with native comparison rather than 27 as text.
-- On nodes the identifier appears three times per row before index entries are
-- counted. The infrastructure tables keep their text/UUIDv4 ids and are NOT
-- migrated: they barely join these tables, and churning working tables and
-- their tests buys nothing.
--
-- Deliberately NOT in this migration:
--   * IPTV programme listings. A 24/7 channel generates thousands of ephemeral
--     entries a month, and running identity, merge and relation machinery over
--     guide data is waste rather than correctness. platform#9 gives listings
--     their own lightweight table keyed to the channel node, refreshed and
--     pruned on its own schedule; that table is a later slice. An
--     iptv_channel is a Node. A programme that airs once is not.
--   * Export state. Because PostgreSQL is authoritative (platform#10), exports
--     are generated on demand from these tables and nothing is maintained
--     live, so no column records where a node was exported to.

-- ---------------------------------------------------------------------------
-- nodes — the containment tree
-- ---------------------------------------------------------------------------
-- One recursive tree of variable depth, where depth is whatever a given work's
-- real structure needs rather than a globally fixed number of levels. A film
-- is Work -> Item. A series is Work -> Container(season) -> Item(episode). A
-- chapter-only manga is Work -> Item today and grows a volume container later
-- by inserting a layer and re-parenting, with nothing else changing.

CREATE TABLE IF NOT EXISTS nodes (
    id             uuid             PRIMARY KEY,

    -- The root Work of this node's tree; for a Work, its own id. Denormalised
    -- so "everything in this work" is one indexed scan and not a recursive
    -- walk. NO ACTION rather than RESTRICT because a Work's row references
    -- itself, and RESTRICT would fire on the row being deleted.
    work_id        uuid             NOT NULL REFERENCES nodes (id) ON DELETE NO ACTION,

    -- NULL exactly for a Work. RESTRICT, never CASCADE: platform#9 rules that
    -- deletion is a decision a user confirms, so removing a parent must fail
    -- rather than silently take a subtree with it.
    parent_id      uuid             REFERENCES nodes (id) ON DELETE RESTRICT,

    -- Closed and Platform-owned: the tree has exactly three structural roles
    -- and traversal code depends on them.
    node_kind      text             NOT NULL
                                    CHECK (node_kind IN ('work', 'container', 'item')),

    -- media_type, container_type and item_type are deliberately UNCONSTRAINED
    -- text. The property this whole model exists to deliver is that adding a
    -- media type is new rows and not new tables; a CHECK listing the known
    -- types would make every new media type a schema migration, which is
    -- precisely the outcome platform#2 and platform#9 rule out. It would also be
    -- wrong today: an artist is its own Work (see below) and "artist" is not
    -- in platform#9's illustrative list. Correctness of these values belongs to
    -- the writing capability, as it does for the JSONB columns.
    media_type     text             NOT NULL,
    container_type text,
    item_type      text,

    title          text             NOT NULL,

    -- Float sort key so 5.5 inserts between 5 and 6 without renumbering
    -- siblings. platform#9 leaves the exact fractional scheme at large scale
    -- unsettled, so the database stores what it is given and rebalances
    -- nothing.
    natural_order  double precision NOT NULL DEFAULT 0,

    -- A node whose last source binding is removed becomes 'orphaned'. That is
    -- not deletion, and nothing here deletes it.
    status         text             NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'orphaned')),

    -- Per-media-type variation lives here instead of in per-type columns.
    -- GIN-indexed below, which makes it queryable but not typed: the schema
    -- does not validate these documents and platform#9 assigns their
    -- correctness to the writing capability.
    external_ids   jsonb            NOT NULL DEFAULT '{}'::jsonb,
    attributes     jsonb            NOT NULL DEFAULT '{}'::jsonb,

    created_at     timestamptz      NOT NULL,
    updated_at     timestamptz      NOT NULL,

    -- A Work is a root and a root is a Work.
    CONSTRAINT nodes_root_is_work
        CHECK ((parent_id IS NULL) = (node_kind = 'work')),
    -- A Work is its own tree's work_id.
    CONSTRAINT nodes_work_is_self
        CHECK (node_kind <> 'work' OR work_id = id),
    -- container_type and item_type are set only for their respective kinds.
    CONSTRAINT nodes_container_type_kind
        CHECK (container_type IS NULL OR node_kind = 'container'),
    CONSTRAINT nodes_item_type_kind
        CHECK (item_type IS NULL OR node_kind = 'item'),
    -- Referenced by parts so the database itself can require that a Part
    -- hangs off an item and not off a work or container.
    CONSTRAINT nodes_id_kind_key UNIQUE (id, node_kind)
);

-- The load-bearing index. "Children of this node, in order" is the single most
-- common query a media browser makes, and it must be a plain indexed scan with
-- no recursion at read time.
CREATE INDEX IF NOT EXISTS nodes_parent_order_idx
    ON nodes (parent_id, natural_order);

CREATE INDEX IF NOT EXISTS nodes_work_order_idx
    ON nodes (work_id, natural_order);

-- Browsing the library: the roots of every tree, by type.
CREATE INDEX IF NOT EXISTS nodes_roots_idx
    ON nodes (media_type, title)
    WHERE parent_id IS NULL;

CREATE INDEX IF NOT EXISTS nodes_external_ids_gin
    ON nodes USING gin (external_ids);

CREATE INDEX IF NOT EXISTS nodes_attributes_gin
    ON nodes USING gin (attributes);

-- ---------------------------------------------------------------------------
-- parts — bytes, editions and segments
-- ---------------------------------------------------------------------------
-- A Part is what actually gets played. An edition or cut is NOT a new Node:
-- Blade Runner 2049 is one Item however many cuts exist, because the cut is a
-- property of which bytes play. Multi-disc releases use the same mechanism
-- with part_role = 'segment', so there is one source-selection path and not
-- two.
--
-- A Part points at bytes and never contains them (platform#10). Primary media is
-- never rewritten, re-containered or moved into a content-addressed store; it
-- stays as whatever it already is, wherever the source keeps it, so a standard
-- player can direct-play it without Mosaic in the path.

CREATE TABLE IF NOT EXISTS parts (
    id                uuid             PRIMARY KEY,

    node_id           uuid             NOT NULL,
    -- Carried solely so the composite foreign key below can assert it. A Part
    -- belongs to an item; the database enforces that rather than trusting
    -- every future writer to remember it.
    node_kind         text             NOT NULL DEFAULT 'item'
                                       CHECK (node_kind = 'item'),

    part_role         text             NOT NULL
                                       CHECK (part_role IN ('edition', 'segment')),
    -- Empty for an unremarkable single file.
    edition_label     text             NOT NULL DEFAULT '',
    natural_order     double precision NOT NULL DEFAULT 0,

    -- Local path or remote provider reference. Both are first-class: a library
    -- may be entirely local, entirely remote, or mixed, and nothing above the
    -- Part cares which.
    location_scheme   text             NOT NULL
                                       CHECK (location_scheme IN ('local', 'remote')),
    location_provider text             NOT NULL DEFAULT '',
    location_ref      text             NOT NULL,

    -- Technical metadata. Every field is optional; the zero value means "not
    -- known", which is the normal state before a probe has run and the
    -- permanent state where a field is meaningless (a CBZ has no audio codec).
    container         text             NOT NULL DEFAULT '',
    video_codec       text             NOT NULL DEFAULT '',
    audio_codec       text             NOT NULL DEFAULT '',
    width             integer          NOT NULL DEFAULT 0,
    height            integer          NOT NULL DEFAULT 0,
    hdr_format        text             NOT NULL DEFAULT '',
    duration_ns       bigint           NOT NULL DEFAULT 0,
    bitrate_bps       bigint           NOT NULL DEFAULT 0,
    size_bytes        bigint           NOT NULL DEFAULT 0,

    attributes        jsonb            NOT NULL DEFAULT '{}'::jsonb,

    created_at        timestamptz      NOT NULL,
    updated_at        timestamptz      NOT NULL,

    CONSTRAINT parts_node_is_item
        FOREIGN KEY (node_id, node_kind) REFERENCES nodes (id, node_kind)
        ON DELETE RESTRICT,
    -- A remote location names its resolving provider; a local one does not.
    CONSTRAINT parts_provider_matches_scheme
        CHECK ((location_scheme = 'remote') = (location_provider <> '')),
    CONSTRAINT parts_location_ref_present
        CHECK (location_ref <> '')
);

CREATE INDEX IF NOT EXISTS parts_node_order_idx
    ON parts (node_id, natural_order);

CREATE INDEX IF NOT EXISTS parts_attributes_gin
    ON parts USING gin (attributes);

-- ---------------------------------------------------------------------------
-- relations — the association graph
-- ---------------------------------------------------------------------------
-- Containment is a tree; association is a graph, and it does not nest.
-- Conflating the two is what makes flat media models accumulate edge cases.
--
-- Three of platform#9's four deliberate non-uniformities are carried by this
-- table rather than by the tree, and the schema must not quietly normalise
-- them away:
--
--   * An ARTIST IS NOT A CONTAINER OF ALBUMS. Box sets, collaborations and
--     various-artist compilations all break single-parent containment. An
--     artist is its own Work, joined to album Works by relations.
--   * A COLLECTED EDITION IS ITS OWN WORK, related to what it collects by
--     'collection_member' — the same mechanism as any other collection. A
--     Collection is likewise not a second concept: it is a Node with
--     media_type = 'collection' and no items of its own.
--   * AN ANIME AND ITS SOURCE MANGA ARE TWO WORKS joined by 'adaptation'.
--     They have different part structures and frequently diverge in canon, so
--     forcing them into one tree would corrupt both.
--
-- relation_type is CHECK-constrained where media_type is not. This is the
-- vocabulary of the graph itself rather than of the things being associated,
-- it is Platform-owned, and specific features read specific types.

CREATE TABLE IF NOT EXISTS relations (
    id            uuid             PRIMARY KEY,
    from_node_id  uuid             NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    to_node_id    uuid             NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,

    relation_type text             NOT NULL
                                   CHECK (relation_type IN (
                                       'adaptation',
                                       'sequel',
                                       'prequel',
                                       'spinoff',
                                       'collection_member',
                                       'alternate_edition_of',
                                       'same_franchise'
                                   )),

    -- Written once at creation. platform#9 records that relation confidence has
    -- no decay or reverification policy, so nothing ages or rechecks this and
    -- there is deliberately no updated_at.
    confidence    double precision NOT NULL
                                   CHECK (confidence >= 0 AND confidence <= 1),

    origin        text             NOT NULL
                                   CHECK (origin IN (
                                       'system_inferred',
                                       'provider_supplied',
                                       'user_confirmed'
                                   )),

    created_at    timestamptz      NOT NULL,

    CONSTRAINT relations_no_self_loop CHECK (from_node_id <> to_node_id),
    CONSTRAINT relations_unique_edge UNIQUE (from_node_id, to_node_id, relation_type)
);

-- Both directions are indexed: "what does this adapt" and "what adapts this"
-- are equally ordinary questions.
CREATE INDEX IF NOT EXISTS relations_from_idx
    ON relations (from_node_id, relation_type);

CREATE INDEX IF NOT EXISTS relations_to_idx
    ON relations (to_node_id, relation_type);

-- ---------------------------------------------------------------------------
-- source_bindings — identity confidence
-- ---------------------------------------------------------------------------
-- Identity resolution is explicit rather than implicit. A merge is a confirmed
-- high-confidence binding. A weak match lands as 'pending_review' and surfaces
-- to the user rather than silently merging two different works that share a
-- title. A split moves a binding to a different node; the source is never
-- re-fingerprinted and nothing else in the graph needs to know.

CREATE TABLE IF NOT EXISTS source_bindings (
    id               uuid             PRIMARY KEY,

    -- RESTRICT: a node with sources behind it cannot be deleted out from under
    -- them. Removing the last binding leaves the node 'orphaned', and deleting
    -- it after that is a separate, confirmed decision.
    node_id          uuid             NOT NULL REFERENCES nodes (id) ON DELETE RESTRICT,

    source_provider  text             NOT NULL,
    source_ref       text             NOT NULL,

    match_confidence double precision NOT NULL
                                      CHECK (match_confidence >= 0 AND match_confidence <= 1),

    match_method     text             NOT NULL
                                      CHECK (match_method IN (
                                          'external_id_exact',
                                          'fingerprint',
                                          'fuzzy_title',
                                          'user_selected'
                                      )),

    status           text             NOT NULL
                                      CHECK (status IN (
                                          'confirmed',
                                          'pending_review',
                                          'rejected'
                                      )),

    created_at       timestamptz      NOT NULL,
    updated_at       timestamptz      NOT NULL,

    -- One source binds to at most one node. This is what makes a split a move
    -- rather than a copy.
    CONSTRAINT source_bindings_source_key UNIQUE (source_provider, source_ref)
);

CREATE INDEX IF NOT EXISTS source_bindings_node_idx
    ON source_bindings (node_id);

-- The review queue. Identity resolution becoming visible means the Platform
-- needs a surface for a user to act on, and this is the read behind it.
CREATE INDEX IF NOT EXISTS source_bindings_pending_idx
    ON source_bindings (created_at)
    WHERE status = 'pending_review';
