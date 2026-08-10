# Capabilities do not own stores

**Status:** Accepted. Supersedes [platform#1](0001-transactional-store-extensibility.md).
**Date:** 2026-07-19

## Context

[platform#1](0001-transactional-store-extensibility.md) replaced the Platform's closed transaction interface with uniform, type-based store resolution — `Store[T](tx)` — so that a capability could join a transaction without Core Platform being edited on its behalf.

It was solving a specific problem: *"a capability that owns durable state it must commit atomically with the outbox had no way to join the transaction."*

[platform#2](0002-module-storage-and-delivery-model.md), accepted afterwards, rules that problem out. Modules do not own storage or schema; the Platform owns the database and exposes a content-agnostic object model that modules write into. Adding anime, manga or music is new rows, not new tables.

The two records were therefore in tension, and the tension was resolved by looking at what a capability actually does. An anime capability sources metadata, searches for shows, and adds them to the Platform for use. Every one of those is a function the Platform already performs, applied to a different media type — which is precisely why the storage architecture was made generic rather than shaped around one media type.

**A capability has no durable state of its own to commit.** The premise of [platform#1](0001-transactional-store-extensibility.md) does not hold.

## Decision

**Capabilities do not own stores.** The set of stores is Platform-owned and closed, and the transaction scope enumerates it.

1. `Tx` keeps its named accessors. Enumerating a closed, Platform-owned set is honest; the accessors describe exactly what exists.
2. **`Store[T](tx)` and its resolver are removed.** Uniform type-based resolution is a service locator: it converts a compile-time dependency into a runtime lookup that can fail. That cost buys nothing once no capability needs to register a store.
3. **The `StorageAdapter` port is retained.** It addresses a different and still-live concern — the backing engine is replaceable, so PostgreSQL is a module rather than a privileged implementation. Nothing here changes that.
4. When the Platform itself grows a store — the node, relation and attribute stores of the content model — it is added to `Tx` deliberately, as Platform evolution.

A capability that genuinely needs a new data-owning domain remains what [platform#2](0002-module-storage-and-delivery-model.md) said it was: a proposal to evolve the Platform and its SDK, decided deliberately, not something a capability introduces on its own.

## Alternatives considered

**Complete the [platform#1](0001-transactional-store-extensibility.md) migration** — seal `Tx`, repoint resolution onto the storage adapter, move every command handler onto `Store[T]`. *Rejected:* it builds machinery for a case that has been ruled out, and ships a runtime failure mode into the public contract surface. `Store[AnimeStore](tx)` would compile and fail only when it ran.

**Leave the migration half-finished**, with `Store[T]` delegating to the accessors. *Rejected:* the delegation is a shim that presents itself as a mechanism. It offers capability authors an extension point that does not extend anything, which is worse than not offering one.

**Keep `Store[T]` for Platform stores only**, as a call-site style. *Rejected:* it adds indirection and a failure mode to solve a naming preference. `tx.Users()` is clearer than `Store[UserStore](tx)` and cannot fail.

**A registry keyed by string or type.** Already rejected by [platform#1](0001-transactional-store-extensibility.md) for losing type safety at the call site, and rejected again here for the same reason plus the one above.

## Consequences

**Less code, and less public surface.** The contract surface promoted into `contracts/platform/v1` no longer needs to carry a resolution mechanism, so the first thing community developers see is smaller.

**No runtime failure mode.** A capability cannot ask for a store that does not exist, because it does not ask for stores at all.

**Adding a Platform store means editing `Tx`.** That is correct rather than a cost: the store set belongs to the Platform, and a change to it is a deliberate Platform change.

**The reference capability's purpose changes.** It was to prove a capability could own a store and join a transaction. It should now prove what a capability actually does — source external metadata, search existing content, create nodes and relations in the generic model, and publish an event, using only published contracts and owning no schema.

**It is blocked on something else than was recorded.** The blocker was believed to be an empty `contracts/platform/v1` and a closed `Tx`. The real blocker is that the content-agnostic object model does not exist: the schema holds identity, sessions, permissions, configuration, events, jobs, diagnostics and a blob registry, and no node tree, relation graph or attribute storage. A capability has nowhere to put an anime.

## Implementation implications

The additive half of [platform#1](0001-transactional-store-extensibility.md) that reached code is reverted: `Store[T]`, its resolver, and the tests proving equivalence between the two paths. The `StorageAdapter` port and the PostgreSQL adapter that implements it are kept. Tests migrated onto `Store[T]` return to the accessors.

The next Platform work is the content-agnostic object model, not this migration.
