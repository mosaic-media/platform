# Claude Instructions — Mosaic Platform

## Source of truth

**The code in this repository is authoritative.** It is ~22,300 lines of Go and it decides what Mosaic is. The [`architecture`](https://github.com/mosaic-media/architecture) repository *describes* it and records the decisions behind it. If the two disagree, the documentation is wrong — fix it there, in the same session, rather than working around it.

**Eight repositories, all siblings on disk** (`../platform`, `../architecture`, `../sdk`, `../module-stremio-addons`, `../mosaic-shell`, `../sdui`, `../mosaic-sdui-react`, `../mosaic-storybook`):

- **`platform`** (this repo) — the Platform: domain, contracts, application services, the PostgreSQL module, transports, the composition root.
- **`architecture`** — the docs and ADRs. Push doc updates here whenever code and docs diverge.
- **`sdk`** — the **published contract surface**, extracted into its own module (`github.com/mosaic-media/sdk`). This is what a Module compiles against. See "The published SDK is a separate module" below — this catches out anyone who assumes the content types are still under `internal/`.
- **`module-stremio-addons`** — the **first optional module**, in its own repo exactly as a third party's would be: a Go client of the Stremio addon protocol importing only the SDK, MIT-licensed, published at `v0.1.0`. The Platform requires it as a tagged dependency (ADR 0019–0021). Commit and push it separately.
- **`mosaic-shell`** — the **Server-Driven-UI web client** (React/TypeScript/Vite, now a package in the `web` workspace repo), not a Module and not in the binary. A thin app: chrome/routing + the SDUI runtime below. Its component set is primitives + `ComponentDefinition` data on a token-driven skin, technology-agnostic so a future Flutter client renders the same payloads. AGPL-3.0-only (ADR 0022–0024). It runs on a **live session over the two-lane Connect `SessionService` (ADR 0041)** — it Subscribes to the Platform and renders the pushed shell + region updates; dev sign-in mints a session over the Connect `AuthService` (ADR 0061). (Static mock screens remain in-tree as dead code.) Commit and push it separately.
- **`mosaic-sdui-react`** — **`@mosaic-media/sdui-react`**, the **React runtime** for the SDUI (primitives, registry, renderer, definition expander, `ShellProvider`, token skin). Extracted from the Shell into its own repo so the Shell and the storybook consume it as **peers**. AGPL-3.0-only (first-party client code, unlike the Apache contract). Builds to `dist` via tsc; React is a peer dep. Published: npm `@mosaic-media/sdui-react@0.1.0`.
- **`mosaic-storybook`** — a **live, bespoke storybook** of the SDUI components (each shown as a live render beside its `UINode` JSON), on GitHub Pages at https://mosaic-media.github.io/mosaic-storybook/. React/TS/Vite, AGPL-3.0-only; a peer consumer of `@mosaic-media/sdui-react` + `@mosaic-media/sdui` from npm. Not part of the runtime path — pure showcase/docs.
- **`sdui`** — the **published SDUI contract** the Platform, Modules and Shell share: JSON-Schema schema (`UINode`/`Action`/`ComponentDefinition`) is the single source of truth; the Go/TS bindings are **generated** from it. Ships a **Go producer binding** (`github.com/mosaic-media/sdui/sdui`, published at `v0.1.0` — `go get` it, the emit-side builds screens against it), an npm package (`@mosaic-media/sdui`), the standard definition library as data, and DTCG tokens. Apache-2.0, like the SDK (ADR 0025). When you build the Platform's SDUI emit-side, `go get` this and build against it (`replace => ../sdui` for local cross-repo work). Commit and push it separately.

Required reading, and it is short:

- **[Architecture](https://github.com/mosaic-media/architecture/blob/main/docs/architecture.md)** — the package map, the invariants, the standing test gates. Read before changing structure.
- **[Roadmap](https://github.com/mosaic-media/architecture/blob/main/docs/roadmap.md)** — where the build is and what the open threads are. The critical path is complete.
- **[Unreachable capability](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md)** — the register of what the Platform can do that nobody can ask it to do. **Read it with the roadmap, not instead of it:** a slice marked done there means the capability landed, not that a user can reach it, and no build or test failure will ever tell you the difference.
- **[Decision records](https://github.com/mosaic-media/architecture/tree/main/docs/adr)** — numbered ADRs. Most load-bearing here: 0007 (static composition), 0012 (capabilities do not own stores), 0013 (the object graph), 0014 (storage authority), 0015 (open vs closed vocabularies), 0016 (the published contract surface), 0017 (how a capability acts).

Everything else is published at [mosaic-media.github.io/architecture](https://mosaic-media.github.io/architecture/), with a PDF of each page.

> **The MDL/MDS/MEG/MAC/MIP/MOP/MAD/MDP specification library no longer exists.** It grew to 200+ largely unvalidated documents, accumulated contradictions faster than they could be resolved, and produced concrete wrong work — a roadmap built against an abandoned DuckDB storage model, and an invented module transport layer the architecture explicitly forbids. It was retired on 2026-07-19 and is preserved only at git tag `pre-reset-2026-07-19`. **Do not cite MEG-015 or any other retired identifier, and do not attempt to read those paths.** If something you need is missing from the documents above, say so rather than reconstructing it.

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
- **Transports call services only**: a transport is a projection surface, not a persistence layer. A handler must call application command or query services — never open a database connection or query Postgres (or any module) directly. Boundary tests in `internal/transport/auth` and `internal/transport/health` enforce it by parsing import declarations.
- **Config reload classes**: every configuration field declares a reload class — `Hot` (applies without restart), `Restart` (requires process restart), `Generation` (requires Supervisor to activate a new Generation), `Recovery` (applies only through recovery flow). Classify new config fields before adding them.

## Transaction shape (ADR 0012 supersedes ADR 0001)

`Tx` enumerates the Platform's stores by name — `Users()`, `Sessions()`,
`Permissions()`, `Config()`, `Outbox()`, `Credentials()`, and the content
stores `Nodes()`, `Parts()`, `Relations()`, `SourceBindings()`. Every store
reached through one `Tx` writes to the same database transaction, so state and
outbox events commit atomically. That is the whole purpose of the type.

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

The four content stores were the first exercise of this rule: growing the
Platform's store set is deliberate Platform evolution and should look like it.

## The published SDK is a separate module

This is the single most surprising thing for a new session. **The content
models and the content application-service API do not live under `internal/`.
They were extracted into their own module** — `github.com/mosaic-media/sdk`,
**published** and required in `go.mod` at `v0.3.0`, resolved from the module
proxy with **no `replace`** (ADR 0016). The tags: `v0.1.0` the content surface,
`v0.2.0` the `Capability` interface (ADR 0019), `v0.3.0` the `ImportRequest`
carrying module settings (ADR 0021). A sibling working tree at `../sdk`
is still handy for local SDK work — add a `replace => ../sdk` temporarily
if you're changing the SDK, then tag/push a new version and bump the require.

- Content types are imported as
  `v1 "github.com/mosaic-media/sdk/contracts/platform/v1"`. `v1.Node`,
  `v1.Part`, `v1.Relation`, `v1.SourceBinding`, their vocabularies (`v1.MediaType`,
  `v1.NodeKind`, …), the content command/query/result types, the
  `v1.ContentService` interface, and the opaque `v1.Caller` all live there.
- **Use the `v1` constants, not `domain`** — `v1.MediaAnimeSeries`,
  `v1.NodeWork`, etc. `NormaliseTypeName` and `Node.Canonical()` are on the
  `v1` types now, not on `domain`.
- What stays internal: the store contracts (`NodeStore`, `Tx`,
  `StorageAdapter`), and the identity/config/event models in
  `internal/platform/domain`. A capability calls application services, never
  stores.
- **Changing the SDK:** edit `../sdk`, then for the Platform to see it
  either add a `replace github.com/mosaic-media/sdk => ../sdk`
  to `go.mod` for local work, or tag and push a new version and bump the
  require. `../sdk` is its own git repo; commit and push it separately.
  The reference capability (`capabilities/reference/`) and `test/sdkprobe/`
  import only the SDK, enforced by boundary tests — the stop point made
  executable: **if a capability needs a private Platform import, the contracts
  are not ready to publish.**

## Workflow

- Develop and commit directly on `main`. This repository does not use feature branches for Platform implementation work.
- **Commit author identity:** every commit in this repository must be authored (and committed) by `AdamNi-7080 <anicholls41@gmail.com>`. This is the repo owner's identity — do not commit as `Claude`/`noreply@anthropic.com` or any other identity. If git has no identity configured on the machine, set it repo-locally (`git config user.name "AdamNi-7080"` / `git config user.email "anicholls41@gmail.com"`), not globally. Keep the `Co-Authored-By: Claude ...` trailer in the message body; it does not change the commit author.
- **Push when the work has been shown to pass, in this conversation.** The rule
  used to be "never push without asking each time", which produced long queues of
  unpushed commits and made the remote a lagging, unreliable picture of the
  build. The bar is now evidence rather than permission: push once the change has
  been *demonstrated* working — `go build`, `go vet`, `go test ./...`, gofmt and
  the license-header check green, and where the change is user-visible, exercised
  against the running stack.
  - **Demonstrated, not asserted.** Tests that were skipped are not tests that
    passed, and "it should work" is not evidence. If the verification could not
    be run, commit locally and say so rather than pushing on optimism.
  - **Force-push still requires asking**, every time. It rewrites history other
    checkouts may hold, and no amount of local green makes that safe to decide
    alone.
- Build one slice at a time, in the order defined by the roadmap. Do not start a slice whose prerequisites have not landed.
- Each slice must pass the standing test gates in the architecture page before the next dependent slice begins.
- Run `go build ./...` and `go test ./...` before declaring any slice done.
- **Every Go file carries an SPDX header** (`AGPL-3.0-only`, the Platform's license). New files get it from the tool, not by hand: `go run ./tools/licenseheader` adds it to any file missing it (pass file paths to limit it to those). **CI enforces it** — `.github/workflows/verify.yml` runs `go run ./tools/licenseheader -check` (plus gofmt/vet/build) on every push and PR, so a headerless file fails the check. A local pre-commit hook adds it for you before the commit — enable once per clone with `git config core.hooksPath .githooks`. Change the header text in one place — the `header` const in that tool.
- Commit per passing slice — one commit (or focused set of commits) per slice, not one commit for the whole build sequence.
- When ambiguity comes up, read the code first, then the three architecture documents. Do not substitute assumption for a decision. If neither answers it, say so — an honest gap is worth more than an invention that reads as settled.

## What is built

**The critical path is complete: the content model, its published surface, a
reference capability that uses only that surface, and the surface's extraction
into the SDK module.** Everything passes `go build ./...`, `go vet ./...` and
`go test ./... -race` against real PostgreSQL 16. Detail is in the git history;
this is the map, oldest first.

| Slice | What it proves |
|---|---|
| Scaffold → Supervisor handoff (twelve slices) | Boot, contracts, the command order, ABAC policy, the PostgreSQL module + migrations, transactional outbox, event bus + worker, config versioning, secret broker, the (since-retired) GraphQL schema, diagnostics, and a graceful serve loop |
| Content model | `nodes`, `parts`, `relations`, `source_bindings` in native `uuid`/UUIDv7 columns; four stores on `Tx`; ADR 0013's four non-uniformities pinned by contract tests |
| Content commands and queries | Nine application services over the graph (search, node read, external-id lookup; add work/child/part, relate, bind, resolve) — the command order into the content model |
| Published contract surface (ADR 0016) | `contracts/platform/v1` populated: content models, the nine services, `ContentService`, opaque `Caller`; store contracts stay internal; a separate module compiles against it (`test/sdkprobe` / `test/sdkboundary`) |
| Reference capability (the thesis test) | `capabilities/reference/` imports only the SDK, sources an anime over HTTP, dedupes, creates the tree + adaptation edge + bindings — proven end to end against PostgreSQL. **The extension model works.** |
| SDK extraction | `contracts/platform/v1` moved out into `github.com/mosaic-media/sdk` at `v0.1.0`; the Platform depends on it as an external module |
| Runnable process | `main.go` constructs `app.Service`; the client API served over HTTP at `:8081` (health handoff on `:8080`); Argon2id password hasher; end-to-end HTTP test signs in and drives a screen |
| Permissions management + bootstrap | `PermissionStore` gained `CreateRole`/`GrantRole` (+ commands; their GraphQL mutations went with ADR 0061 — see below); `internal/composition/bootstrap.EnsureAdmin` seeds a first admin idempotently from env vars, so the binary is usable by a human |
| Optional-module composition + invocation (ADR 0019, 0020) | The SDK gained a `Capability` interface (`Manifest()`/`Import()`) at `v0.2.0`; the Platform gained a `CapabilityRegistry`, an `ImportContent` command (action `content.import`), invoked as the `importContent` action. The **Stremio module** (its own repo `module-stremio-addons`, importing only the SDK) is statically composed in via `main.go` and invoked through the registry — sourcing movies/series from a Stremio addon and landing the tree + source binding + **`RemoteLocation` stream Parts** in PostgreSQL. Proven end to end. **The composition-and-invocation half of the extension story works.** |
| User-managed module settings (ADR 0021) | The first SDK gap the Stremio module surfaced. A Platform-owned `ModuleSettingsStore` (one jsonb doc per module id, on `Tx`), generic `configureModule`/`moduleSettings` commands (actions `module.configure`/`module.read`), and SDK `v0.3.0` handing a module its settings via `ImportRequest{Caller, Query, Settings}`. A user adds a Stremio addon by manifest URL at runtime; the module reads `{"addons":[...]}`. Retired the `MOSAIC_STREMIO_ADDONS` env bridge. |
| One client transport (ADR 0061) | GraphQL is deleted. A new `mosaic.auth.v1.AuthService` (`SignIn`/`SignOut`, `internal/transport/auth`) mints the session that `SessionService` spends, so Connect is the *only* client transport. `internal/transport/rpc` maps the seven error categories onto Connect status codes — which GraphQL never actually did — and holds the telemetry interceptor for both services. The retained "external/tooling" surface ADR 0041 kept turned out to have no caller, so it went rather than being re-ported; the session transport's `dispatch` switch is now the complete list of what a client can invoke. |

**Reverted long ago:** uniform store resolution (`Store[T]`) under ADR 0012.

**Running it:** set `MOSAIC_POSTGRES_DSN`, and (optionally)
`MOSAIC_BOOTSTRAP_ADMIN_USERNAME` + `MOSAIC_BOOTSTRAP_ADMIN_PASSWORD`, then
`go run ./cmd/mosaic-platform`. It migrates, seeds the admin, registers the
built-in modules, and serves the whole client API on `:8081` over h2c — the
Connect `AuthService` that mints a session (ADR 0061) and the two-lane Connect
`SessionService` that spends it (ADR 0041) — plus artwork at `:8081/artwork`,
playback at `:8081/playback/`, and the Supervisor handoff on `:8080`. There is
no GraphQL endpoint: ADR 0061 deleted it. The Stremio module is always
registered; a user adds addons at runtime via the `configureModule` action
(ADR 0021) — the `MOSAIC_STREMIO_ADDONS` env var is retired.

## What is not built

- **An admin surface — and read the register before assuming otherwise.**
  Creating users, creating roles, granting them, drafting/validating/activating
  config versions and setting user status all have application services, policy
  actions and passing tests — and no way for a user to reach them. The full,
  classified list is
  **[Unreachable capability](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md)**
  in the architecture repo; consult it before telling anyone Mosaic can do
  something, because the code and the tests will not tell you.
  **`app.CreateLocalUser` is the case that proves the point:** a complete,
  well-tested command — boundary order, policy denial, unauthenticated
  rejection, real-PostgreSQL integration — whose *only callers are its own
  tests*. No transport has ever exposed it, so Mosaic has never had a way to
  create a second user. That predates ADR 0061 and nothing in this repository
  reports it.
  These return as server-emitted screens (ADR 0029) whose affordances dispatch
  through `Invoke`. `bootstrap.EnsureAdmin` (ADR 0018) stays the only in-band
  way to establish the first authority. `SignOut` is implemented and tested but
  has no caller — the Shell has no sign-out affordance.
  **If you delete a client path, add its row to that register in the same
  change.**
- **`ContentService` relation reads.** The service can create edges
  (`RelateContent`) but not read them back — there is no `ListFrom`/`ListTo`
  on the published surface. A capability that wants to query relations can't
  yet. Small, additive; a candidate `v1` addition (a `v0.x` bump).
- **The `media_types` registry** (ADR 0015). Normalisation collapses spelling
  variants; it cannot catch a value that was never a real type
  (`animeseries`). The registry needs the **capability manifest shape**
  (undecided) and a capability that introduces a genuinely new media type
  (the anime reference capability uses only known ones). Deferred; ADR 0015
  is amended to say so.
- **Module-granular permissions and a system principal.** User permissions
  management is built; a capability acts as its invoking user (ADR 0017).
  Authority a *module* holds distinct from that user, and a system principal
  for background (no-user) work, are scoped to future ADRs.
- **External-module *distribution*.** The optional-module *shape* now exists
  (its own Go module importing only the SDK, statically composed and invoked —
  the Stremio module, ADR 0019/0020). What is unbuilt is how a third-party
  module is *discovered and selected*: the Supervisor's build-time module
  selection and generated `imports.go` (ADR 0007), signing and trust tiers.
  The composition root registers modules explicitly, standing in for that.
- **Play-time stream resolution / transcoding.** The Stremio module
  *snapshots* a stream location into a `RemoteLocation` Part at import;
  *resolving* and transcoding those bytes at play time is a separate future
  module (the "Remote Media" module) and is the roadmap's deferred "streaming"
  concern. `ContentService` is import-only — it has no resolve/play surface.
- **IPTV programme listings.** ADR 0013 gives them their own lightweight table
  keyed to the channel node; unbuilt.
- **Jobs service.** `jobs`, `job_attempts`, `job_logs` are empty tables.
  `SELECT ... FOR UPDATE SKIP LOCKED` is the intended pattern.
- **`LISTEN`/`NOTIFY`.** The outbox worker polls; notifications would be an
  accelerator over the poll, not a replacement.
- **Session refresh and device pairing.** Resolvers return `Unavailable`.
  Same for jobs and health-history resolvers.
- **Exports (NFO/.mos), streaming.** (The SDUI emit-side and the ADR 0041
  session transport are now built — `internal/transport/screens` renders screens
  and `internal/transport/session` serves the two-lane `SessionService`; the web
  Shell renders them live. Play-time stream resolution/transcoding remains the
  deferred "streaming" concern above.)

## What is next

No single forced next step — the critical path is done. The open threads,
roughly in order of how cheaply they harden what exists:

1. **Close the relation-read gap** — add `ListFrom`/`ListTo` to
   `ContentService` (and `v1`), so a capability can read the graph it writes.
   Smallest; would bump the SDK to `v0.1.1`/`v0.2.0`.
2. **A second capability/module** — a different media type (music, comics)
   built against the SDK, to stress the surface and surface what a real second
   consumer needs before the SDK stabilises. The Stremio module (movies + TV)
   is the first; a second from a different angle is the next pressure test.
3. **The rest of the module system** — the walking skeleton (ADR 0019/0020)
   started it with a minimal `Manifest` and explicit registration; what remains
   is growing the manifest shape, the Supervisor's build-time module *selection*
   and distribution (signing, trust tiers), and module-granular permissions.
   This unblocks the `media_types` registry. Larger; several ADRs.
4. **Module-declared cron/jobs (the second SDK gap the Stremio module found).**
   A module needs to register recurring work the Platform runs — cleanup,
   periodic refresh. This converges three deferred pieces: the **jobs runner**
   (`jobs`/`job_attempts`/`job_logs` tables exist, no service; `SELECT ... FOR
   UPDATE SKIP LOCKED` intended), a **scheduler/recurrence** layer (none yet;
   the jobs table is one-shot), and the **system principal** (ADR 0017's named
   gap — a no-user job has no session `Caller` to forward). SDK shape: a module
   declares scheduled jobs (Manifest or a `Jobs()` method) with a handler; the
   scheduler enqueues durable rows on cron; the worker dispatches to the handler
   with a system `Caller`. Larger; its own multi-ADR slice.

### Standing facts a new session needs

- **Content vocabularies are open text, canonicalised on write** (ADR 0015).
  The test for open-vs-closed is "does Platform code branch on it?" —
  `node_kind`, `part_role`, relation types, match methods, statuses are
  `CHECK`-constrained; `media_type`, `container_type`, `item_type` are not.
  Stores call `v1.Node.Canonical()`, a contract obligation, so `Anime Series`
  and `anime-series` are one type. Use `v1` constants, not string literals.
- **How an optional module is composed and invoked** (ADR 0019, 0020). A
  module is its **own Go module and its own repository** — the Stremio module
  now lives in the sibling repo
  [`module-stremio-addons`](https://github.com/mosaic-media/module-stremio-addons)
  (`../module-stremio-addons` on disk, module path
  `github.com/mosaic-media/module-stremio-addons`), importing **only** the SDK —
  enforced by a boundary test and by Go itself. It implements the SDK
  `Capability` interface. `main.go`'s `registerCapabilities` constructs it and
  registers it into an `app.CapabilityRegistry`; the platform `go.mod` requires
  it at a tagged version (`v0.1.0`) from the module proxy, no `replace`. A caller
  invokes it through the `ImportContent` command (the `importContent` action),
  which authorises `content.import`, resolves the capability by id, and hands
  it the `app.Service` as its `ContentService` plus the caller — so the
  module's own writes each re-authorise as the invoking user (ADR 0017).
  Explicit registration stands in for ADR 0007's eventual Build-Pipeline
  `imports.go`. Distinct from `internal/modules/` (built-in, trusted, required,
  e.g. Postgres) and `capabilities/reference/` (a package *inside* the platform
  module, not its own).
- **`RemoteLocation` Parts are now exercised.** The Stremio module snapshots a
  stream URL/magnet into a Part with `Scheme: v1.RemoteLocation, Provider:
  "stremio"` (ADR 0014's remote path, previously unused). Metadata and streams
  are independent: a meta-only addon yields Works + tree with **no** Parts, so
  Stremio metadata can enrich local media without adopting remote streaming.
- **Module settings are user-managed, opaque JSON** (ADR 0021), *not* the
  platform Config system (which is operator config with reload classes).
  `ModuleSettingsStore` holds one jsonb doc per module id (on `Tx`); the
  Platform stores it uninterpreted and hands it to the module on invocation
  (`v1.ImportRequest.Settings`); the module owns its meaning. Set via
  `configureModule`, read via `moduleSettings`. **Modules are built to find SDK
  gaps** — this was the first (user-entered addon URLs). The next identified gap
  is module-declared cron/jobs, which needs the jobs runner, a scheduler, and
  the **system principal** (ADR 0017's named gap, for no-user work).
- **UUIDv7 for content ids.** `NewIDGenerator()` (UUIDv4) serves the
  infrastructure tables; `NewUUIDv7Generator()` (`ContractSet.ContentIDs`)
  serves the content tables. Content ids are native `uuid`; infrastructure
  ids stay `text`/UUIDv4 and are not migrated.
- **SQLSTATE `23001` → `Conflict`** (explicit `ON DELETE RESTRICT`).
- **Password hashing is Argon2id** in `internal/adapters/crypto`, PHC-encoded;
  satisfies `domain.PasswordVerifier` structurally.
- **Left unbuilt, not invented** (ADR 0013): the fractional ordering scheme at
  scale, relation confidence decay (edges written once; `RelationStore` has no
  `Update`), and attribute validation (JSONB is unvalidated by design).
- **The stop point still governs any SDK change:** if a capability needs a
  private Platform import, the contracts are not ready to publish.
