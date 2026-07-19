# Claude Instructions — Mosaic Platform

## Source of truth

**The code in this repository is authoritative.** It is ~15,300 lines of Go and it decides what Mosaic is. The [`mosaic-architecture`](https://github.com/mosaic-media/mosaic-architecture) repository *describes* it and records the decisions behind it. If the two disagree, the documentation is wrong — fix it there, in the same session, rather than working around it.

Required reading, and it is short:

- **[Architecture](https://github.com/mosaic-media/mosaic-architecture/blob/main/docs/architecture.md)** — the package map, the invariants, the standing test gates. Read before changing structure.
- **[Roadmap](https://github.com/mosaic-media/mosaic-architecture/blob/main/docs/roadmap.md)** — the build sequence, slice exit criteria and the stop point before SDK work.
- **[Decision records](https://github.com/mosaic-media/mosaic-architecture/tree/main/docs/adr)** — numbered ADRs. 0007 (static composition), 0012 (capabilities do not own stores), 0013 (the object graph) and 0014 (storage authority) bear most directly on this repository.

Everything else is published at [mosaic-media.github.io/mosaic-architecture](https://mosaic-media.github.io/mosaic-architecture/), with a PDF of each page.

> **The MDL/MDS/MEG/MAC/MIP/MOP/MAD/MDP specification library no longer exists.** It grew to 200+ largely unvalidated documents, accumulated contradictions faster than they could be resolved, and produced concrete wrong work — a roadmap built against an abandoned DuckDB storage model, and an invented module transport layer the architecture explicitly forbids. It was retired on 2026-07-19 and is preserved only at git tag `pre-reset-2026-07-19`. **Do not cite MEG-015 or any other retired identifier, and do not attempt to read those paths.** If something you need is missing from the three documents above, say so rather than reconstructing it.

## Package tier model

Three tiers. This is the layout; it is also documented in the architecture page.

1. **Core Platform** — `internal/platform/*`
   Domain, contracts, application services. Fully trusted, compiled in, defines the rules everything else follows.

2. **Built-in module** — `internal/modules/*`
   Infrastructure that implements Platform contracts using the same registration and manifest shape a future external Module would use, but compiled in, required, and fully trusted — not sandboxed, not independently versioned, not optional.
   Postgres is the first example: it lives at `internal/modules/postgres/` (**not** `internal/adapters/postgres/`), registered through `internal/composition/builtin/` the same way an external Module would be discovered.

3. **External module** (future)
   Product/domain capability packs — anime, manga, etc. Governed, independently versioned, discovered at runtime. **Not** part of this repo's initial scaffold.

`internal/adapters/` is reserved for things that are **not** module-shaped — helpers that don't implement a full contract surface on their own (filesystem utilities, crypto helpers). Do not put Postgres there.

Modules compile into the binary, so there is no runtime boundary between Platform and Module code and no in-process sandbox. That is a deliberate trade of isolation for speed (ADR 0007). Treat module trust as something established before the build, not enforced at runtime.

## Non-negotiable rules

- **Dependency direction**: dependencies point inward. Transport → Application services → Contracts/Domain. Adapters/Modules → Contracts → External systems. Domain must never import transport, adapter, or database packages. Application services may depend on Platform contracts, never on concrete Postgres (or other module) types.
- **Error categories**: every contract error maps to one of `InvalidArgument`, `Unauthenticated`, `PermissionDenied`, `NotFound`, `Conflict`, `Unavailable`, `Internal`. Adapters/modules may keep driver-specific errors internally; application services and transports must only ever see these Platform categories.
- **Command handler order**: every command handler follows the same sequence — validate command shape → authenticate caller → authorize via policy → open a `UnitOfWork` → load state through contracts → apply domain rules → persist state and outbox events in the same transaction → return a Platform result type.
- **GraphQL resolvers call services only**: GraphQL is a transport and projection surface, not a persistence layer. Resolvers must call application command or query services — never open a database connection or query Postgres (or any module) directly.
- **Config reload classes**: every configuration field declares a reload class — `Hot` (applies without restart), `Restart` (requires process restart), `Generation` (requires Supervisor to activate a new Generation), `Recovery` (applies only through recovery flow). Classify new config fields before adding them.

## Transaction shape (ADR 0012 supersedes ADR 0001)

`Tx` enumerates the Platform's stores by name — `Users()`, `Sessions()`,
`Permissions()`, `Config()`, `Outbox()`, `Credentials()`. Every store reached
through one `Tx` writes to the same database transaction, so state and outbox
events commit atomically. That is the whole purpose of the type.

ADR 0001 replaced these accessors with uniform `Store[T](tx)` resolution so a
capability could join a transaction without Core Platform being edited for it.
**ADR 0012 supersedes that**: capabilities do not own stores. An anime
capability sources metadata, searches and adds content through the Platform's
generic content model — functions the Platform already performs, applied to a
different media type. It owns no schema, so it has no store to register.

The uniform-resolution machinery has therefore been removed:

- `Store[T]` and its resolver are gone. It was a service locator with a runtime
  failure mode, solving a case that does not occur.
- `Tx` keeps its named accessors. Enumerating a closed, Platform-owned set is
  honest.
- The `StorageAdapter` port is **kept** — engine replaceability is a separate,
  still-live concern, and PostgreSQL remains a module rather than a privileged
  implementation.

When the Platform grows a store — the node, relation and attribute stores of
the content model — add it to `Tx` deliberately. That is Platform evolution and
should look like it.

## Workflow

- Develop and commit directly on `main`. This repository does not use feature branches for Platform implementation work.
- **Commit author identity:** every commit in this repository must be authored (and committed) by `AdamNi-7080 <anicholls41@gmail.com>`. This is the repo owner's identity — do not commit as `Claude`/`noreply@anthropic.com` or any other identity. If git has no identity configured on the machine, set it repo-locally (`git config user.name "AdamNi-7080"` / `git config user.email "anicholls41@gmail.com"`), not globally. Keep the `Co-Authored-By: Claude ...` trailer in the message body; it does not change the commit author.
- **Do not push unless explicitly asked.** Commit locally on `main`; leave pushing to the remote (and any force-push) to an explicit request from the owner each time.
- Build one slice at a time, in the order defined by the roadmap. Do not start a slice whose prerequisites have not landed.
- Each slice must pass the standing test gates in the architecture page before the next dependent slice begins.
- Run `go build ./...` and `go test ./...` before declaring any slice done.
- Commit per passing slice — one commit (or focused set of commits) per slice, not one commit for the whole build sequence.
- When ambiguity comes up, read the code first, then the three architecture documents. Do not substitute assumption for a decision. If neither answers it, say so — an honest gap is worth more than an invention that reads as settled.

## What is built

Twelve slices, each proven against a real PostgreSQL 16 and passing
`go build ./...`, `go vet ./...` and `go test ./... -race`. Detail is in the
git history; this is the map.

| Slice | What it proves |
|---|---|
| Repository scaffold | Process boots, packages compile |
| Core contracts | First contract set in `internal/platform/contracts/`, seven error categories, contract identity metadata |
| Application service skeleton | The command order runs end to end against fakes, with a call-order trace and rollback |
| Identity, sessions and policy | Real ABAC `policy.Engine` (not a stub) drives every decision; a denied action mutates nothing |
| PostgreSQL adapter and migrations | Built-in module at `internal/modules/postgres/`, eleven embedded migrations, a fail-fast migrator, SQLSTATE mapped to Platform categories, and app services running unchanged against the real adapter |
| Transactional outbox | State and event commit atomically — proven by failing mid-transaction and querying raw tables to confirm neither row persists |
| In-process Event Bus | `Bus` + `Worker`, at-least-once delivery, exponential backoff, dead-letter after eight attempts, retries surviving restart |
| Configuration versioning | Draft to Validated to Active, reload classes, and at most one Active version enforced by a unique partial index |
| Secret broker | OS keychain with an AES-256-GCM vault fallback, `secret://` references, and a static check that nothing reads secret files directly |
| GraphQL surface | Executable schema where every resolver body is one call into `app.Service`, enforced by a test that parses imports |
| Diagnostics and health | Real component state, redact-by-default logging, and support bundles proven not to leak a planted secret |
| Supervisor handoff | Five HTTP endpoints, a real serve loop, and graceful shutdown that drains the outbox — proven with a one-hour ticker so only the shutdown drain can deliver |
| Content model | `nodes`, `parts`, `relations`, `source_bindings` in native `uuid`/UUIDv7 columns; four stores added to `Tx`; the four ADR 0013 non-uniformities pinned by contract tests |

**Reverted:** uniform store resolution and its PostgreSQL follow-up, under
ADR 0012. `Store[T]` solved for capability-owned stores, which ADR 0002 had
already ruled out. No production code ever called it.

## What is not built

- **Command handlers over the content model.** The stores exist and are
  proven against real PostgreSQL, but no application service commands them
  yet, so there is no validate → authenticate → authorize → transact path
  into the graph. The `Tx` fakes in `internal/platform/app` and
  `internal/transport/graphql` return nil for the four content stores for
  exactly this reason.
- **IPTV programme listings.** ADR 0013 gives them their own lightweight
  table keyed to the channel node. An `iptv_channel` is a Node; a programme
  that airs once is deliberately not, and that table is still unbuilt.
- **Module permissions.** The policy engine governs *user* authority. Module
  authority is undecided.
- **External modules.** Only the built-in shape exists.
- **Jobs service.** `jobs`, `job_attempts` and `job_logs` are tables with
  nothing on them. `SELECT ... FOR UPDATE SKIP LOCKED` is the intended pattern.
- **`LISTEN`/`NOTIFY`.** The outbox worker polls. Notifications would be an
  accelerator, not a replacement — they are dropped when no listener is
  connected, so the poll stays as the floor.
- **HTTP serving for GraphQL.** The schema is executable and tested, not served.
- **Session refresh and device pairing.** Resolvers for both return
  `Unavailable` rather than faking success. Same for jobs and health history.
- **Exports, Shell, SDUI, streaming.**

## What is next

**The reference capability path.** The content model landed, so a capability
now has somewhere to put an anime — the blocker that failed that slice twice
is gone.

Under ADR 0012 it must prove a capability can source external metadata, search
existing content, create nodes and relations, and publish an event, using only
published contracts and owning no schema. That will likely want content-model
command handlers first, since the capability should go through application
services rather than reaching for `Tx` itself.

Then SDK extraction readiness.

### Notes carried out of the content-model slice

- **Open and closed vocabularies are now ADR 0015**, which refines ADR 0013.
  The test is whether Platform code branches on the value. `node_kind`,
  `part_role`, relation types, match methods and statuses do, so they are
  `CHECK`-constrained. `media_type`, `container_type` and `item_type` do not,
  so they are unconstrained text — a `CHECK` would make every new media type a
  schema migration. Nothing validates the open columns, so a typo fragments a
  library silently; a `media_types` registry is the named fix, due when
  something other than Platform code can introduce a type. Use the `domain`
  constants, not string literals.
- **UUIDv7 has its own generator.** `NewIDGenerator()` stays UUIDv4 for the
  infrastructure tables; `NewUUIDv7Generator()` serves the content tables and
  is exposed as `ContractSet.ContentIDs`. Both satisfy `contracts.IDGenerator`.
- **SQLSTATE `23001` now maps to `Conflict`.** PostgreSQL reports an explicit
  `ON DELETE RESTRICT` as `23001`, not `23503`; without it a refused cascading
  delete surfaced as `Internal`.
- **Unsettled by ADR 0013 and left unbuilt, not invented:** the fractional
  ordering scheme at large scale (`natural_order` is stored as given and
  nothing rebalances), relation confidence decay or reverification (edges are
  written once, and `RelationStore` has no `Update` so that stays visible),
  and attribute validation (JSONB is unvalidated by design; correctness
  belongs to the writing capability).

The stop point still holds: **if the reference capability needs a private
import, the contracts are not ready to publish.**
