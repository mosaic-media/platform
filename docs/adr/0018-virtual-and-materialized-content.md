# Virtual browse and materialized library

**Status:** Accepted
**Date:** 2026-07-20

## Context

[sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md) gives modules read-side
provider roles — search, catalog, metadata, stream. That immediately raises a
storage question the write-only import model never had to answer: **what lands
in the object graph, and when?**

The naïve answer is "everything a provider returns," and it is wrong. A Stremio
catalog addon exposes thousands of titles across many collections, and a stream
addon returns dozens of stream references per title. Reflecting a source's
catalogs into `nodes` on browse, or snapshotting every stream a provider knows,
would flood the object graph with content no one curated and references that go
stale the moment they are written. The object graph is a *library* — the curated
set shown to every Mosaic user ([platform#9](0009-object-graph.md)) — not a mirror
of every source.

But search and browse must still work over source content that is not in the
library yet — that is the entire point of searching. So there must be content
the Platform can present and act on without persisting it.

## Decision

**Content exists in two planes. The virtual plane is what a provider returns on
read — transient, never persisted. The materialized plane is the object-graph
library — written only by an explicit, curated act. A provider result crosses
from one to the other exactly once, by materialization.**

**The virtual plane.** `SearchProvider` and `CatalogProvider` return
`SearchResult` / `CatalogItem` values (SDK types, [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)):
a provider id, a native id, a media type, a title, artwork, and enough to
render a row and to materialize later. These are DTOs, not nodes — they hit no
store, carry no `NodeID`, and are recomputed on each read. A user searching
Mosaic, or an admin opening a collection, reads the virtual plane live through
the providers. This is why collections do **not** auto-populate the object
graph: browsing is a read, not a write.

**Materialization is the one crossing, and it is curated.** Two acts move a
virtual result into the library, and only these two:

- an **admin selects a collection** (or items within it) in the admin portal to
  publish into the library for all users; and
- a **user picks a search result** to add.

Materialization takes the virtual item's reference and calls the existing write
path — `AddContentWork` and the tree builders, plus a source binding on the
native id ([platform#9](0009-object-graph.md)) — through `MetadataProvider` for
the full detail. It reuses [platform#15](0015-module-capability-and-invocation.md)'s
import: import *is* materialization, now driven by a chosen result rather than a
typed id. Re-materializing a result already in the library is the idempotent
no-op import already is, resolved by the source binding.

**Streams snapshot onto the materialized set.** Because materialized content is
bounded by curation — an admin published *these* collections, not the whole
source — attaching stream references to those items is safe and keeps reads
simple. A materialized item gets its `RemoteLocation` stream Parts
([platform#10](0010-storage-authority-and-transaction-scope.md)) from
`StreamProvider`, exactly as the Stremio module does today. The flood the
virtual plane prevents is *un*curated content; a curated library carrying its
own stream references is the intended state, not the failure mode.

**Search is a union.** A user's query fans out to the `SearchProvider`s and to
the local library, and results are merged and de-duplicated by source binding,
each marked *in library* or *virtual*. The user sees one list; picking a virtual
result materializes it, picking a library result opens it.

## Alternatives considered

**Persist everything a provider returns.** *Rejected:* it makes the object graph
a stale mirror of every source, floods it with uncurated content, and defeats
the library's meaning as the curated set ([platform#9](0009-object-graph.md)).

**Persist a lightweight "virtual node" row for every browsed item, distinct from
library nodes.** *Rejected:* it re-introduces the flood one table over, and adds
a second content model the whole Platform would have to branch on. Transient
DTOs cost nothing between reads.

**Resolve streams lazily at play time instead of snapshotting at
materialization.** *Considered and not taken now.* Lazy resolution avoids stale
references and is the natural home of the future Remote Media / play-time
resolution work. But materialized content is already bounded by curation, so the
snapshot does not threaten the store, and snapshotting keeps reads and the
current module behavior simple. Lazy resolution remains the likely evolution
when play-time resolution is built; this ADR keeps the snapshot and records the
seam.

**Let the object graph hold catalogs as containers.** *Rejected:* a source's
catalog is a *view* it computes (Popular, Trending), not a stable collection with
identity in our graph. Materializing a catalog copies its *items* into the
library; it does not import the catalog as an object.

## Consequences

The "overwhelm the database" concern is answered structurally, not by tuning:
uncurated content never reaches a store because browse is a read. The library
stays what [platform#9](0009-object-graph.md) says it is — the curated set — and
its size is governed by admin and user choices, not by how large the sources are.

Search needs no raw ids: the virtual plane *is* the discovery surface, and
materialization is how a discovered thing becomes durable. Metadata enrichment
of an existing node is the same `MetadataProvider` read without the write.

Two honest limits:

1. **Virtual reads are as available as the provider.** A search or browse is a
   live call to an addon; if the source is down, the virtual plane for it is
   empty, and only the materialized library remains. This is correct — the
   library is the durable part — but it means browse latency and reliability
   track the providers, and a caching story is future work.
2. **Snapshotted streams go stale.** A materialized item's stream Parts are a
   point-in-time snapshot; a magnet or URL that dies is not noticed until
   resolved. Lazy re-resolution (above) is the eventual fix; until then a
   re-materialize refreshes them.

## Implementation implications

The Platform gains: a search service that fans out to `SearchProvider`s and
unions with the library; a catalog-browse service over `CatalogProvider`s for the
admin portal; a materialize service that turns a chosen virtual result into
library nodes via `MetadataProvider` and snapshots streams via `StreamProvider`;
and GraphQL for each (search, browse-catalogs, materialize-selection). The user
search screen and the admin collection browser are the first real SDUI screens
([contracts#1](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0001-server-driven-ui-and-the-shell.md)), so this work and the SDUI
emit-side converge. The Stremio module gains its `catalog` and `search` provider
implementations; its `meta` and `stream` paths move behind `MetadataProvider`
and `StreamProvider` unchanged.
