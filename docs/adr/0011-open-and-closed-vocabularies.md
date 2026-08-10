# Open and closed vocabularies in the object graph

**Status:** Accepted. Refines [platform#9](0009-object-graph.md).
**Date:** 2026-07-19

## Context

[platform#9](0009-object-graph.md) describes `media_type` as naming "the kind of thing" and then lists ten examples — `movie`, `tv_series`, `anime_series`, `album`, `book`, `manga_series`, `comic_series`, `podcast`, `iptv_channel`, `collection`. It does not say whether that list is exhaustive. The same silence covers `container_type` and `item_type`.

Implementing the model forced the question, and the answer turned out to be already latent in [platform#9](0009-object-graph.md) twice over.

First, its Consequences state: **"Adding a media type is rows, not tables. That is the property the whole model exists to deliver, and it is what makes a community-built module possible without Platform changes."** A `CHECK` constraint enumerating the known types would make every new media type a schema migration, which is exactly the property being denied.

Second, the list is provably incomplete. [platform#9](0009-object-graph.md)'s own first non-uniformity states that an artist is its own Work joined to album Works by Relation — and `artist` is not among the ten. A closed list would have made the decision unimplementable on the day it was written.

Underneath the ambiguity, two different kinds of vocabulary had been conflated. Some of these columns name **what the Platform's machinery does** and are read by Platform code to decide behaviour. Others name **what a thing is**, and the Platform never branches on them. Those two need opposite treatment, and neither [platform#9](0009-object-graph.md) nor the implementation had said so.

## Decision

**Vocabulary that the Platform branches on is closed and constrained. Vocabulary that only describes content is open and unconstrained.**

The test: *does Platform code read this value to decide what to do?* If yes it is structural, belongs to the Platform, and a new value is a Platform change. If no, it is descriptive, belongs to whoever is cataloguing, and a new value is data.

**Open — unconstrained text, no `CHECK`:**

- `media_type` — `movie`, `anime_series`, `artist`, and whatever a module brings
- `container_type` — `season`, `volume`, `arc`, `disc`, `box_set`, …
- `item_type` — `episode`, `track`, `chapter`, `issue`, …

The constants the Platform declares for these are a starting vocabulary for its own use, not a closed set.

**Closed — `CHECK`-constrained:**

- `node_kind` (`work`, `container`, `item`) — traversal depends on it
- `part_role` (`edition`, `segment`) — source selection depends on it
- `relation_type` — specific features read specific edge types
- `origin`, `match_method` — identity resolution machinery
- node and binding `status` values — state machines

The JSONB `attributes` and `external_ids` columns follow the open rule for the same reason, as [platform#9](0009-object-graph.md) already established: the schema does not validate them, and correctness belongs to the writing capability.

### Open does not mean unguarded

An open vocabulary invites a specific failure: `anime_series`, `anime-series` and `Anime Series` are three distinct values that browse as three separate libraries, and a user discovers it by wondering where half their anime went. Openness is a decision about *who may add a type*, not a licence to let one concept fragment into several.

Two mechanisms address it, and they are not alternatives — they cover different halves.

**Normalisation, now.** Values are stored canonically: lowercased, with any run of separators collapsed to a single underscore. `Anime Series`, `anime-series` and `ANIME_SERIES` all persist as `anime_series`. This is a function rather than a constraint, so a new media type still needs no migration, and it is a contract obligation rather than an adapter detail — any implementation of `NodeStore` owes it, and writes return the canonical value. A colon survives, so a future module-supplied type can namespace itself (`animekit:ova`).

The distinction that matters: a shape `CHECK` would *reject* `anime-series`, leaving the author to guess the house style. Normalisation *converts* it, which is what the user actually needs.

**A registry, with the reference capability.** Normalisation collapses spelling variants of a correct concept. It cannot recover `animeseries`, which has no separator to fix, or `anmie_series`, which is simply wrong. Only a closed-by-membership check catches those: a Platform-owned `media_types` table with a foreign key from `nodes.media_type`, seeded by the Platform and contributed to by a module through the manifest it already declares. That keeps [platform#9](0009-object-graph.md)'s phrase literally true — adding a media type is a row.

**It lands when a capability introduces a media type the Platform does not already know.** The registry's value is a module declaring a *new* type through its manifest and the Platform acting on it, so its consumer is a capability that brings a type not in the seeded vocabulary.

> **Amended 2026-07-19.** The original wording tied the registry to "the reference capability slice." That slice has since landed, and the reference capability — an anime importer — uses only known media types (`anime_series`, `manga_series`), so it never introduces a novel one and does not exercise the registry. The registry also depends on the capability manifest shape, which is still undecided (see the overview's *Deliberately Undecided*). So it waits on two things now: the manifest mechanism a module declares a type through, and a capability that actually introduces one. Until then, normalisation is the whole of the enforcement, and the narrow gap below stands.

Until then a value that was never a real type still lands silently. That is a known, bounded gap: every media type today is written by Platform constants or by a capability in this repository, where a typo is a bug caught in review rather than shipped to a user.

## Alternatives considered

**A closed `CHECK` on the media vocabulary.** Referential safety immediately, and the simplest thing that could work. *Rejected:* it makes every new media type a schema migration, contradicting [platform#2](0002-module-storage-and-delivery-model.md) and [platform#9](0009-object-graph.md)'s stated purpose, and it is already wrong today — `artist` is required by [platform#9](0009-object-graph.md)'s own non-uniformity and absent from its list. It would also put the Platform in the position of ratifying media types, which is the coupling the generic object model exists to avoid.

**Building the `media_types` registry now.** Correct destination, wrong time. *Rejected:* it forces answers on uninstall semantics and orphaned-type handling while there is no external module system to give those answers meaning. Recorded above as anticipated, with an explicit trigger, so the intent is not lost.

**A shape constraint** — `CHECK (media_type ~ '^[a-z][a-z0-9_]*$')`. *Rejected in favour of normalisation, which is strictly better at the same cost.* A constraint rejects `anime-series` and leaves the author to guess what was wanted; normalisation converts it to the right value. The constraint also risks reintroducing the original problem in miniature: if module-supplied types later want namespacing, a strict pattern blocks it and relaxing it becomes the migration this ADR exists to avoid.

**Normalising and stopping there**, treating the registry as unnecessary. *Rejected:* normalisation cannot recover a missing separator or a misspelling, so the larger failure — a value that was never a real type — would stay open indefinitely rather than being owed to a named slice.

**Treating every one of these columns as open**, including `node_kind` and `part_role`. Uniform and simple to state. *Rejected:* Platform code switches on those values. An unrecognised `node_kind` is not a new media type, it is a traversal that does not know what it is looking at, and it should fail at write time rather than at read time.

## Consequences

**Adding a media type stays free**, which is the property [platform#9](0009-object-graph.md) exists to deliver and the precondition for a community module introducing a format the Platform has never heard of.

**The Platform never ratifies a media type.** It has no list to be added to and no opinion to express, which is what keeps a module from needing a Platform change to ship.

**Spelling variants converge; invented types do not, until the registry lands.** Normalisation makes the common case safe and the remaining gap narrow: a value that was never a real type still persists silently. That is a known, bounded gap rather than an oversight, and it is owed to a named slice rather than to "later". A capability writing media types should use constants rather than string literals regardless.

**Writes may return a value different from the one passed in.** Stores canonicalise, so a caller writing `Anime Series` reads back `anime_series`. This is deliberate and contract-level rather than adapter-specific, and it means normalisation must be idempotent — a read-modify-write cycle cannot be allowed to drift.

**There is now a rule for future columns.** When the object graph grows a column with a small set of allowed values, the open/closed question has an answer — ask whether Platform code branches on it — instead of being settled per column by whoever implements it.

**[platform#9](0009-object-graph.md)'s illustrative lists stay illustrative.** They are the Platform's starting vocabulary, and the absence of `artist` from them is a gap in the example rather than a constraint on the model.
