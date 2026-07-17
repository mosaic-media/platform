# Claude Instructions — Mosaic Platform

## Source of truth

- The [`mosaic-architecture`](https://github.com/mosaic-media/mosaic-architecture) repository (`mosaic-media/mosaic-architecture`) is the canonical source for all Mosaic design and architecture decisions. This repository implements what's specified there — it does not redefine it.
- **MEG-015 — Platform Foundation Implementation** is the primary build guide for this repository's first implementation. Read it in full, in build order, before starting any slice.
- Required reading for Platform implementation work, per MEG-015 §00 (Document Control):
  - MAC-001 — Platform Architecture
  - MEG-001 — Go Engineering Standards
  - MEG-004 — Hexagonal Architecture
  - MEG-005 — Runtime Architecture
  - MEG-006 — Module Platform
  - MEG-007 — Storage Architecture
  - MEG-009 — Security Architecture
  - MIP-006 — Generation Composition Protocol
- Whenever a section in MEG-015 (or any other spec) references another document by ID — MEG-005, MEG-009, MIP-004, etc. — open that document in `mosaic-architecture` and read the relevant section before implementing. Never guess at its contents or substitute general engineering knowledge for what a linked spec actually says.

## Package tier model (correction to MEG-015 §02)

MEG-015 §02 — Repository Layout describes a two-tier model: `internal/platform/*` (private Platform code) and `internal/adapters/*` (with Postgres under `internal/adapters/postgres/`). Before implementation began on this repo, that layout was corrected to **three tiers**. Use this model, not MEG-015 §02's original layout:

1. **Core Platform** — `internal/platform/*`
   Domain, contracts, application services. Fully trusted, compiled in, defines the rules everything else follows.

2. **Built-in module** — `internal/modules/*`
   Infrastructure that implements Platform contracts using the same registration/manifest shape a future external Module would use (per MEG-006), but compiled in, required, and fully trusted — not sandboxed, not independently versioned, not optional.
   Postgres is the first example: it lives at `internal/modules/postgres/` (**not** `internal/adapters/postgres/`), registered through `internal/composition/builtin/` the same way an external Module would be discovered.

3. **External module** (future, per MEG-006)
   Product/domain capability packs — anime, manga, etc. Governed, independently versioned, discovered at runtime. **Not** part of this repo's initial scaffold.

`internal/adapters/` is reserved for things that are **not** module-shaped — helpers that don't implement a full contract surface on their own (filesystem utilities, crypto helpers). Do not put Postgres there.

**Outstanding:** this is a correction made before implementation began, not yet reflected in MEG-015 itself. MEG-015 §02 should be updated to match the next time a documentation sync pass runs against `mosaic-architecture`. Do not silently edit MEG-015 from an implementation session in this repo — flag it and let a docs session handle it.

## Non-negotiable rules (from MEG-015)

- **Dependency direction** (§02): dependencies point inward. Transport → Application services → Contracts/Domain. Adapters/Modules → Contracts → External systems. Domain must never import transport, adapter, or database packages. Application services may depend on Platform contracts, never on concrete Postgres (or other module) types.
- **Error categories** (§03): every contract error maps to one of `InvalidArgument`, `Unauthenticated`, `PermissionDenied`, `NotFound`, `Conflict`, `Unavailable`, `Internal`. Adapters/modules may keep driver-specific errors internally; application services and transports must only ever see these Platform categories.
- **Command handler order** (§04): every command handler follows the same sequence — validate command shape → authenticate caller → authorize via policy → open a `UnitOfWork` → load state through contracts → apply domain rules → persist state and outbox events in the same transaction → return a Platform result type.
- **GraphQL resolvers call services only** (§09): GraphQL is a transport and projection surface, not a persistence layer. Resolvers must call application command or query services — never open a database connection or query Postgres (or any module) directly.
- **Config reload classes** (§08): every configuration field declares a reload class — `Hot` (applies without restart), `Restart` (requires process restart), `Generation` (requires Supervisor to activate a new Generation), `Recovery` (applies only through recovery flow). Classify new config fields before adding them.

## Workflow

- Build one slice at a time, in the order defined by MEG-015 §12 — Build Sequence. Do not start a slice whose prerequisites haven't landed.
- Each slice must pass its MEG-015 §11 test gate before the next dependent slice begins.
- Run `go build ./...` and `go test ./...` before declaring any slice done.
- Commit per passing slice — one commit (or focused set of commits) per slice, not one commit for the whole build sequence.
- When ambiguity comes up, check Required Reading (and whatever spec the ambiguous area maps to) before guessing. Do not substitute assumption for a documented decision.

## Current status

Slices from MEG-015 §12 — Build Sequence, all unchecked:

- [ ] Repository scaffold — includes creating `internal/modules/` as well as `internal/adapters/` per the tier correction above; Postgres does not belong under `internal/adapters/`.
- [ ] Core contracts
- [ ] Application service skeleton
- [ ] Identity, sessions and policy
- [ ] PostgreSQL adapter and migrations
- [ ] Transactional outbox
- [ ] In-process Event Bus
- [ ] Configuration and secret broker
- [ ] GraphQL command and query surface
- [ ] Diagnostics and health
- [ ] Supervisor handoff
- [ ] Reference capability path
- [ ] SDK extraction readiness
