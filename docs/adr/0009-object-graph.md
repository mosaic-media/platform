# The object graph: Node, Part, Relation

**Status:** Accepted. Refined by [platform#11](0011-open-and-closed-vocabularies.md), which settles whether the type vocabularies below are open or closed.
**Date:** 2026-07-19

## Context

[platform#2](0002-module-storage-and-delivery-model.md) established that the Platform owns a content-agnostic object model and that modules map onto it rather than extending the schema — adding anime, manga or music must be new rows, not new tables. It deferred the model's actual shape.

That shape is the hard part. A flat object with sources and parts works for a film and breaks on everything else: a manga series with volumes *and* an ongoing chapter-only series with no volumes yet, a comic run with collected editions, a music library with box sets and various-artist compilations, an anime with a related but separately-structured source manga. Bolting each on as a special case is how media libraries accumulate edge-case bugs for a decade.

Two distinct relationships are being conflated in that flat design. **Containment** — a season contains episodes — is a tree. **Association** — this anime adapts that manga, these films belong to one collection — is a graph, and it does not nest.

## Decision

Three structures, plus a binding table.

### Node — the containment tree

A single recursive tree of **variable depth**, where depth is whatever a given work's real structure needs rather than a globally fixed number of levels.

- `node_kind` is `work`, `container` or `item`.
- `media_type` names the kind of thing — `movie`, `tv_series`, `anime_series`, `album`, `book`, `manga_series`, `comic_series`, `podcast`, `iptv_channel`, `collection`.
- `container_type` (`season`, `volume`, `arc`, `disc`, `box_set`) and `item_type` (`episode`, `track`, `chapter`, `issue`, `feature`, `extra`) are set only for their respective kinds.
- `natural_order` is a float sort key, so `5.5` inserts between 5 and 6 without renumbering siblings.
- `external_ids` and `attributes` are JSONB, indexed with GIN — this is where per-media-type variation lives instead of in per-type columns.

A film is `Work → Item`. A series is `Work → Container(season) → Item(episode)`. A chapter-only manga is `Work → Item` today and grows a volume container later by inserting a layer and re-parenting; nothing else changes.

The load-bearing index is `btree(parent_id, natural_order)` — "children of this node, in order" is the single most common query a media browser makes, and it must be a plain indexed scan with no recursion at read time.

### Part — bytes, editions and segments

A Part is what actually gets played, attached to an item Node. It carries technical metadata (container, codecs, resolution, HDR, duration, bitrate) and a pointer to the bytes.

**An edition or cut is not a new Node.** *Blade Runner 2049* is one Item however many cuts exist; the cut is a property of which bytes play, which is what `part_role = edition` and `edition_label` express. Multi-disc releases use the same mechanism with `part_role = segment`, so there is one source-selection path rather than two.

### Relation — the association graph

Typed, directed, confidence-scored edges: `adaptation`, `sequel`, `prequel`, `spinoff`, `collection_member`, `alternate_edition_of`, `same_franchise`. Each carries `confidence` and an `origin` of `system_inferred`, `provider_supplied` or `user_confirmed`.

A Collection is not a second concept. It is a Node with `media_type = collection` and no items of its own, joined to its members by `collection_member` edges. Computing a grouping is a background job writing Relation rows; reading it back is an indexed join on the same engine as everything else.

### Source bindings — identity confidence

Identity resolution is explicit rather than implicit. A binding carries `match_confidence`, a `match_method` (`external_id_exact`, `fingerprint`, `fuzzy_title`, `user_selected`) and a `status`.

A merge is a confirmed high-confidence binding. A weak match lands as `pending_review` and surfaces to the user rather than silently merging two different works that share a title. A split moves a binding to a different node; the source is never re-fingerprinted and nothing else in the graph needs to know.

When a node's last binding is removed it becomes `orphaned`, not deleted. Deletion is a decision a user confirms, never a silent cascade.

### Identifiers

**UUIDv7, stored in PostgreSQL's native `uuid` type.**

Random identifiers scatter btree inserts across the index, causing page splits and cache misses at the row counts this model targets; time-ordered identifiers append near the right-hand edge. ULID gives the same ordering property but has no native PostgreSQL type, so it costs 27 bytes as `text` with string comparison, or loses readability as `bytea`. Native `uuid` is 16 bytes with native comparison and native driver support. On `nodes` alone the identifier appears three times per row — `id`, `parent_id`, `work_id` — before counting index entries.

The existing infrastructure tables use random UUIDv4 in `text` columns. They are not migrated: they barely join the content tables, and churning twenty-five working tables and their tests buys nothing.

### Deliberate non-uniformities

Forcing every media type through one shape is its own bug. Four cases are modelled against the grain on purpose:

- **Artists are not containers of albums.** Box sets, collaborations and various-artist compilations break single-parent containment. An artist is its own Work, joined to album Works by Relation.
- **Collected editions are their own Work**, related to what they collect by `collection_member` — the same mechanism as any other collection.
- **An anime and its source manga are two Works** joined by `adaptation`. They have different part structures and frequently diverge in canon, so forcing one tree would corrupt both.
- **IPTV programme listings never become Nodes.** A 24/7 channel generates thousands of ephemeral entries a month; running identity, merge and relation machinery over guide data is waste, not correctness. Listings live in their own lightweight table keyed to the channel node, refreshed and pruned on their own schedule.

## Alternatives considered

**A schema per media type** — films table, episodes table, chapters table. *Rejected:* every new media type becomes a schema migration and new query paths, which is precisely what [platform#2](0002-module-storage-and-delivery-model.md) rules out. It also makes cross-type features — search, collections, continue-watching — a union across N tables that grows forever.

**A fixed three-level hierarchy** — work, container, item, always. *Rejected:* it forces empty containers for films and chapter-only manga, and cannot express a volume layer appearing later without restructuring.

**A flat object with special cases bolted on**, the shape this replaces. *Rejected:* it is the design whose edge cases accumulate indefinitely, and it cannot express association at all.

**Editions as separate Nodes.** *Rejected:* it doubles the tree for a distinction that is about which bytes play, and it splits watch progress and identity across rows that are the same work.

**Modelling IPTV listings as Nodes** for uniformity. *Rejected:* the cost is enormous and the benefit is zero — nobody merges, relates or resolves the identity of a programme that airs once.

## Consequences

**Adding a media type is rows, not tables.** That is the property the whole model exists to deliver, and it is what makes a community-built module possible without Platform changes.

**Variable depth costs discipline.** Code must not assume a node has a parent, or that a work's children are containers. Every traversal is by `parent_id`, never by an assumed level.

**JSONB carries per-type variation**, which means it is not validated by the schema. Attribute correctness is the writing capability's responsibility, and GIN indexes make it queryable but not typed.

**Two ordering concerns stay separate.** `natural_order` is a float to allow insertion without renumbering; the exact fractional scheme at large scale is not settled here.

**Relation confidence has no decay or reverification policy.** Edges are written once with a confidence score, and nothing yet ages or rechecks them.

**Identity resolution becomes visible.** Weak matches queue for review rather than resolving silently, which means the Platform needs a surface for a user to act on them.
