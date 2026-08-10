# Storage authority, media linking and transaction scope

**Status:** Accepted
**Date:** 2026-07-19

## Context

Introducing the object graph ([platform#9](0009-object-graph.md)) forces a question that could previously be left alone: when the same information exists in a database and in files on disk, **which one is true?**

An earlier draft leaned toward the filesystem — metadata packages as canon, with the database as a rebuildable projection and recovery defined as replaying an event log against a re-walk of those packages. That is a large commitment. It makes every catalogue write a write to two systems that can disagree, and requires an answer for what happens when they do.

Two further questions travel with it. Where do the media bytes live, given a library may mix files on a local disk with streams resolved through a remote service? And when bounded contexts each own their tables, what does a single transaction span?

## Decision

### PostgreSQL is authoritative

The database is the source of truth for the catalogue. Nothing on disk overrides it, and recovery is a database restore rather than a filesystem re-walk.

### Media is linked, never absorbed

A Part points at bytes; it does not contain or copy them. The pointer is either a local filesystem path or a remote reference resolved through a provider — a debrid service, for example. **The Platform does not care which**, and both are first-class: a library may be entirely local, entirely remote, or mixed, and nothing above the Part cares.

Primary media is never rewritten, re-containered or moved into a content-addressed store. It stays as whatever it already is — MKV, MP4, FLAC, EPUB, CBZ — wherever the source keeps it, so any standard player can direct-play it without Mosaic in the path.

### Two export formats, two different jobs

Because the database is authoritative, exports exist so that data is not trapped inside it. They are generated on demand and read back on import; neither is consulted during normal operation.

- **NFO** exports to *other systems* — Plex, Jellyfin and anything else that reads the convention. It only makes sense for local files, since another system cannot resolve a Mosaic remote reference.
- **`.mos`** carries a library *between Mosaic instances* without a database. It can include remote references, because the receiving Mosaic knows how to resolve them.

Neither is canon. Both are projections of authoritative state.

### One transaction spans one context plus the outbox

Bounded contexts each own their tables and do not read across into another context's. A single transaction therefore covers **the acting context's stores and the shared event outbox**, and nothing else.

Work that spans contexts is two transactions joined by an event, not one transaction touching both. Catalogue importing a node and Access recording progress against it are separate commits; the second happens because the first published an event.

This confirms the reading [platform#2](0002-module-storage-and-delivery-model.md) deferred.

## Alternatives considered

**Filesystem canon, database as projection.** Portable by construction, and survives losing the database entirely. *Rejected:* it makes every write a dual-write to two systems with no shared transaction, so they can diverge with no authority to arbitrate. It also puts file parsing on the critical path of recovery, and turns a corrupted or hand-edited file into a correctness problem rather than an import problem.

**Database authoritative, no export at all.** Simplest. *Rejected:* it locks a user's library inside one system, which is the opposite of what a self-hosted media platform should do, and makes migrating away from Mosaic a data-extraction exercise.

**One export format serving both purposes.** *Rejected:* the two audiences need different things. An external system needs a convention it already understands and cannot use remote references; another Mosaic wants full fidelity including them. Compromising on one format serves neither well.

**Primary media in a content-addressed store**, alongside artwork and subtitles. *Rejected:* it breaks direct play, since bytes would no longer sit at a path a player can open, and it duplicates terabytes to gain deduplication that media libraries rarely benefit from.

**A single transaction spanning contexts.** Simpler to write. *Rejected:* it makes context boundaries advisory rather than real, and the first cross-context transaction becomes the precedent for every later one.

## Consequences

**No dual-write hazard, and no reconciliation to build.** The database commits or it does not.

**Exports are cheap and stateless.** They are generated from authoritative state on request, so the fragment-granularity problem an earlier draft solved — avoiding hundreds of thousands of tiny per-item files kept continuously in sync — does not arise. Nothing is maintained live.

**A node does not need to remember where it was exported to.** Any column recording an owning package path is unnecessary under this decision.

**Recovery is ordinary database operations** — backup, restore, point-in-time recovery — rather than a bespoke rebuild path. The outbox already guarantees no accepted event is lost across a restart.

**Losing the database loses the catalogue** unless a backup or a recent `.mos` export exists. That is the accepted cost, and it makes backup an operational requirement rather than an optional nicety.

**Remote-only libraries work**, which local-file-shaped designs typically do not support without special cases.

**Cross-context work is eventually consistent.** A progress update lands after the import event is delivered, not within the import transaction. Anything requiring both to be immediately consistent belongs in one context.
