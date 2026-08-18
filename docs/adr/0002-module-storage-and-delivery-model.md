# Module storage and delivery model

**Status:** Accepted, and **superseded in part** by [platform#92](0092-module-storage-is-granted-not-enforced.md): point 2's rule that modules do not own storage stands as an intent, but it was a guarantee only while a module was a library compiled into the binary. An extension module is a separate process and can bring its own store, so the Platform now grants bounded storage and reports a containment posture rather than asserting exclusivity. Point 1's premise — that a module is always compiled in — was itself superseded by the extension tier.
**Date:** 2026-07-18

## Context

The module and storage model was correct in spirit across several documents but stated nowhere as one accepted whole, and one earlier document still described a two-database design that had been moved past.

Four questions needed settling together:

1. **What is a Module, concretely?** A build artefact, a runtime plugin, or a service? The answer governs isolation, delivery and storage.
2. **Do Modules own storage?** If a content module — anime, manga, music — can define its own tables, the Platform fragments into many schemas and the single consistency domain collapses.
3. **What separates a community Module from an essential one?** If the difference is architectural, the two are not equals and the ecosystem is second-class.
4. **Is analytical processing a second mandatory database?** An earlier design mandated DuckDB alongside PostgreSQL, fixing an engine count into the architecture before any workload justified it.

## Decision

**1. A Module is a Go library compiled into the binary.** The Supervisor compiles the selected Modules into a single Platform Binary at build time; that binary is a Generation. There are no runtime plugins, dynamic libraries, or RPC sidecars between local Modules. The finished binary draws no boundary between Platform and Module code.

**2. Modules do not own storage or schema.** The Platform owns the database, connection lifecycle, migrations, transactions, access policy, backup boundary and the schema itself. Modules persist through Platform-owned storage contracts.

The schema is designed to make this practical: a **content-agnostic object model** — a recursive node tree, a separate relation graph, engine-neutral identity, flexible per-type attributes — so a new content Module maps onto existing structure. **Adding anime, manga or music is new rows, not new tables.**

A genuinely new *data-owning domain*, as opposed to new data within the existing model, is Platform and SDK evolution, decided deliberately — not something a Module introduces on its own.

**3. Community and essential Modules differ only in delivery.** Both are Go libraries, both use the SDK and Platform contracts, both are compiled into the binary, both are admitted the same way.

| | Essential | Community |
|---|---|---|
| Repository | Ships in the Platform repository | Its own repository |
| Acquisition | Pulled with the Platform | Selected by the user; Supervisor downloads it |
| Selectability | Required for a valid Generation | Optional |
| Architecture | Identical | Identical |

The PostgreSQL storage adapter is the first essential Module.

**4. Analytical processing is a port, not an engine.** Recommendations, correlation, reporting, popularity and search candidates sit behind a Platform-owned analytical processing port. PostgreSQL satisfies it today through materialised views, relation edges and background jobs. If PostgreSQL cannot carry the load alone, an additional engine is added as an essential Module implementing the same port.

## Alternatives considered

**Module-owned storage.** The single consistency domain collapses into many schemas; atomicity across a Module's data and the shared outbox is no longer guaranteed; backup, migration and access policy fragment per Module. *Rejected* — the schema is made content-agnostic instead, so Modules do not need their own.

**An architectural distinction between community and essential Modules.** Creates a second-class ecosystem and a private path — the same drift [platform#1](0001-transactional-store-extensibility.md) removed for storage. *Rejected.*

**A mandatory second analytical database.** Fixes an engine count into the architecture before the workload justifies it, and adds an operational dependency, a second consistency boundary and a sync path. *Rejected in this form* — the analytical *capability* is mandated behind a port; the *engine count* is not.

**Runtime plugin loading.** Reintroduces a runtime boundary, a plugin ABI and dynamic-loading failure modes. *Rejected* — delivery differences resolve at build time, not runtime.

## Consequences

**Enables.** One consistency domain, so atomicity, backup and migration remain single concerns. A genuine ecosystem — publishing anime support is the same shape of work as the built-in PostgreSQL adapter. Deferred engine decisions, since the port absorbs a later second engine. Content growth without schema growth.

**Costs.** The Platform must own a genuinely content-agnostic schema; if that model is not general enough, Modules are squeezed, so its design is load-bearing. A community Module cannot solve an unsupported storage need by reaching for its own table — such a need surfaces as a Platform evolution request, deliberately, rather than being absorbed silently. Analytical work is constrained to what the port exposes.

**Not addressed here.** The concrete identifier scheme and ordering strategy for the object model. How several Modules of the same capability cooperate — union or priority routing — which is a capability-routing question, not a storage one.
