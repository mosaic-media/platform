# Claude Instructions — Mosaic Platform

## Source of truth

**The code in this repository is authoritative.** It is ~37,500 lines of Go and it decides what Mosaic is. The [`architecture`](https://github.com/mosaic-media/architecture) repository *describes* it and records the decisions behind it. If the two disagree, the documentation is wrong — fix it there, in the same session, rather than working around it.

**Ten repositories, all siblings on disk** (`../platform`, `../architecture`, `../sdk`, `../sdui`, `../web`, `../module-stremio-addons`, `../module-aiostreams`, `../module-remote-playback`, `../module-tmdb`, `../module-cinemeta`):

- **`platform`** (this repo) — the Platform: domain, contracts, application services, the PostgreSQL module, transports, the composition root.
- **`architecture`** — the docs and ADRs, including the roadmap this repository's work is measured against. Push doc updates here whenever code and docs diverge.
- **`sdk`** — the **published contract surface** (`github.com/mosaic-media/sdk`). This is what a Module compiles against. Hand-written Go, no dependencies. See "The published SDK is a separate module" below — this catches out anyone who assumes the content types are still under `internal/`.
- **`sdui`** — the **published SDUI and session contracts** the Platform, Modules and clients share. **Protobuf** (`proto/**/*.proto`) generated into Go and TypeScript (ADR 0044), plus a Go producer binding and the `ui` authoring layer, the standard definition library as data, and DTCG tokens. Apache-2.0, like the SDK (ADR 0025). Do not confuse its form with the SDK's: that one is hand-written Go and this one is generated.
- **`web`** — the **frontend workspace** (ADR 0042): the `shell` (the Server-Driven-UI client), `sdui-react` (the published React runtime, `@mosaic-media/sdui-react`) and `storybook`, as three packages in one repository. The three former repositories are archived. AGPL-3.0-only. Not a Module and not in the binary.
- **`module-stremio-addons`** — the first optional module, in its own repo exactly as a third party's would be: a Go client of the Stremio addon protocol importing only the SDK, MIT-licensed. It fills the source-side provider roles (ADR 0027, 0037).
- **`module-aiostreams`** — a **dedicated** stream provider for one named upstream (ADR 0076), beside the addon host above. It fills `stream`, `subtitles` and `settings_ui` and deliberately no read role, so it can never put a title into the library; it is reached only through the enrichment fan-out (ADR 0073). MIT-licensed.
- **`module-remote-playback`** — the first **consumer** module (ADR 0045): it resolves a Part into a playable location and never serves bytes. MIT-licensed.
- **`module-tmdb`**, **`module-cinemeta`** — the two **core** metadata modules (ADR 0062's guarantee clause, ADR 0072). MIT-licensed.

Each module is its own repository, committed and pushed separately. **Only the
core modules are `go.mod` dependencies of the Platform** (`module-tmdb`,
`module-cinemeta`, `module-remote-playback`), required at a tagged version with
no `replace` and compiled into the binary. **Extension modules are not Platform
dependencies at all** (ADR 0081): `module-stremio-addons`, `module-aiostreams`
and `module-fanart-tv` are installed at runtime and adopted by the extension
Manager, so they appear in neither `go.mod` nor the composition root, and the
platform test suite must not import them either (a fake stands in — see
`internal/modules/postgres/fake_capability_test.go`).

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
- **Command handler order**: every command handler follows the same sequence — validate command shape → authenticate caller → authorize via policy → open a `UnitOfWork` → load state through contracts → apply domain rules → persist state and outbox events in the same transaction → return a Platform result type. **Steps 2 and 3 are `Service.enter`** (ADR 0066), which runs both gates once and returns an `authorized`. An internal helper takes that `authorized` and reads stores directly; only an entry point takes a `v1.Caller`. Calling a public `Service` method from inside a handler re-runs the whole boundary — that is what made one search cost ten authenticate-plus-authorize cycles. The rule is enforced, not merely documented: `boundary_conformance_test.go` asserts every caller-bearing method refuses an unknown session and an ungranted caller, and a reflection pass fails the build if a new one is added without a row.
- **Transports call services only**: a transport is a projection surface, not a persistence layer. A handler must call application command or query services — never open a database connection or query Postgres (or any module) directly. Boundary tests in `internal/transport/auth` and `internal/transport/health` enforce it by parsing import declarations.
- **A new screen never needs a client change, and never a component written in a client.** Screens in `internal/transport/screens` are composed from the SDUI vocabulary that already exists, through the generated `ui` builders. **The SDUI has to allow it.** If a screen cannot be expressed, that is a finding about the vocabulary, and there are exactly two honest answers: a new **definition** — authored as data in `mosaic-sdui`'s `definitions/*.json`, which the Platform serves from `definitions.Library()` and which costs no client release — or a deliberate growth of the **native vocabulary** (a primitive, a style field, an action kind), specced in the contract so every client can implement it, with a `@mosaic-media/sdui-react` bump and a roadmap entry. Never a bespoke component or CSS rule added to `web` beside the screen that wanted it; see `web/CLAUDE.md` and `contracts/CLAUDE.md` for the rule in full and what it cost when it was ignored.
- **Author with the generated builders, not `ui.Component` and `ui.Prop`.** `ui.ExtensionCard(name, ui.Summary(…))` is checked against the contract; `ui.Component("ExtensionCard", ui.Prop("summary", …))` is a string that compiles whatever you spell. Reach for the generic constructor only for a type the spec does not cover yet — and then add it to the spec. A prop nothing renders is the quiet version of the same mistake: `ui.Subtitle` on a `Stack` silently drew nothing for the whole life of the extensions screen, because `Stack` has no subtitle and a props bag accepts anything.
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
**published** and required in `go.mod` at a tagged version, resolved from the
module proxy with **no `replace`** (ADR 0016). It is pre-1.0 and bumps additively
whenever a module needs something — `v0.1.0` was the content surface, `v0.2.0`
the `Capability` interface (ADR 0019), and it has grown steadily since. **Read
`go.mod` for the version in use rather than trusting a number written here.** The
SDK's own `README.md` Status section is the per-version changelog. A sibling
working tree at `../sdk`
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

## Everything runs in the container, nothing runs on the host

**Do not run `go build`, `go test`, `go vet`, `go run`, or any Mosaic binary
directly on this machine.** Every gate in this repository runs inside the test
container:

```bash
docker compose -f docker-compose.test.yml run --rm test
```

That is the whole gate — license headers, gofmt, `go vet`, `go build`, `go
test` — in the order `.github/workflows/verify.yml` runs them. Append `bash` to
the same command for a shell in that environment when iterating on one package.

**The reason is that this repository's two most important test dependencies
fail soft.** Neither turns a run red when it is missing. Both let it pass while
testing far less than it appears to:

- **PostgreSQL.** `internal/modules/postgres/harness_test.go` skips every
  contract test, with a reason, when no database is reachable. On a host with
  none, `go test ./...` prints `ok` having exercised no storage code at all.
  The fallback it tries first is an *embedded* PostgreSQL, which refuses to run
  as root — so the naive containerised version skips too. The compose file
  points `MOSAIC_TEST_POSTGRES_DSN` at a real service precisely to convert
  those skips back into tests.
- **ffprobe.** Playback probing shells out to it (ADR 0050). Absent, the
  Platform relays unprobed — a behaviour change rather than an error, and a
  release with undecodable audio then plays silently.

This is the same distinction the push rule below turns on: **demonstrated, not
asserted.** A skipped test is not a passed test, and the container is what makes
that difference structural rather than something each session must remember.

**To run the Platform itself, use the dev stack, not `go run`** — see "Running
the Platform" below.

## Workflow

- Develop and commit directly on `main`. This repository does not use feature branches for Platform implementation work.
- **Commit author identity:** every commit in this repository must be authored (and committed) by `AdamNi-7080 <anicholls41@gmail.com>`. This is the repo owner's identity — do not commit as `Claude`/`noreply@anthropic.com` or any other identity. If git has no identity configured on the machine, set it repo-locally (`git config user.name "AdamNi-7080"` / `git config user.email "anicholls41@gmail.com"`), not globally. Keep the `Co-Authored-By: Claude ...` trailer in the message body; it does not change the commit author.
- **Push when the work has been shown to pass, in this conversation.** The rule
  used to be "never push without asking each time", which produced long queues of
  unpushed commits and made the remote a lagging, unreliable picture of the
  build. The bar is now evidence rather than permission: push once the change has
  been *demonstrated* working — the test container green
  (`docker compose -f docker-compose.test.yml run --rm test`, which covers the
  license-header check, gofmt, vet, build and the tests), and where the change is
  user-visible, exercised against the running dev stack.
  - **Demonstrated, not asserted.** Tests that were skipped are not tests that
    passed, and "it should work" is not evidence. If the verification could not
    be run, commit locally and say so rather than pushing on optimism.
  - **Force-push still requires asking**, every time. It rewrites history other
    checkouts may hold, and no amount of local green makes that safe to decide
    alone.
- Build one slice at a time, in the order defined by the roadmap. Do not start a slice whose prerequisites have not landed.
- Each slice must pass the standing test gates in the architecture page before the next dependent slice begins.
- Run the test container before declaring any slice done. Nothing is declared
  done on the strength of a host-side build, which on this machine is not
  available to be right or wrong about — there is no Go toolchain installed.
- **Every Go file carries an SPDX header** (`AGPL-3.0-only`, the Platform's license). New files get it from the tool, not by hand, and the tool runs in the container like everything else: `docker compose -f docker-compose.test.yml run --rm test go run ./tools/licenseheader` adds it to any file missing it (pass file paths to limit it to those). **CI enforces it** — `.github/workflows/verify.yml` runs `go run ./tools/licenseheader -check` (plus gofmt/vet/build) on every push and PR, so a headerless file fails the check. A local pre-commit hook adds it for you before the commit — enable once per clone with `git config core.hooksPath .githooks`. Change the header text in one place — the `header` const in that tool.
- Commit per passing slice — one commit (or focused set of commits) per slice, not one commit for the whole build sequence.
- When ambiguity comes up, read the code first, then the three architecture documents. Do not substitute assumption for a decision. If neither answers it, say so — an honest gap is worth more than an invention that reads as settled.

## Running the Platform

**In the dev stack, not with `go run`.** It brings its own PostgreSQL, its own
ffmpeg, and the Shell, already wired to each other:

```bash
docker compose -f docker-compose.dev.yml up
```

Add `-f docker-compose.local.yml` after the first `-f` file to build against the
sibling working copies of `sdk`, `sdui` and the modules instead of their
published versions — that overlay writes a `go.work` inside the container only,
which is why switching between published and local changes no committed file.

The stack sets `MOSAIC_POSTGRES_DSN` and the
`MOSAIC_BOOTSTRAP_ADMIN_USERNAME` + `MOSAIC_BOOTSTRAP_ADMIN_PASSWORD` pair for
you. The Platform then migrates, seeds the admin, registers the
built-in modules, and serves the whole client API on `:8081` over h2c — the
Connect `AuthService` that mints a session (ADR 0061) and the two-lane Connect
`SessionService` that spends it (ADR 0041) — plus artwork at `:8081/artwork`,
playback at `:8081/playback/`, and the Supervisor handoff on `:8080`. There is
no GraphQL endpoint: ADR 0061 deleted it. A fresh stack registers only the
**core** modules; Stremio and the other extension modules are **not** composed
in (ADR 0081) — a user installs one at runtime through the `installExtension`
action, and the Platform adopts it across restarts. Once installed, its addons
are configured through the `configureModule` action (ADR 0021), the same as
before; the `MOSAIC_STREMIO_ADDONS` env var is retired.

## The roadmap and the decision records

These rules are identical in every Mosaic repository. They exist because the
state of the build and the reasons behind it are the two things that rot fastest
and report nothing when they do — no build fails, no test goes red.

### The roadmap is maintained, not consulted

**`docs/roadmap.md` in [`architecture`](https://github.com/mosaic-media/architecture)
is the single record of where the build is.** Read it before starting work, and
**update it in the same session as the change that dates it** — not in a
follow-up, which does not happen.

- **A slice that lands is marked landed, with what was left out.** "Built" with
  no qualifier is a claim that the whole slice shipped; if part of it did not,
  say which part and why in the same sentence.
- **Implementation that departs from the plan is recorded where it departed.**
  The roadmap is derived from the code, not from the intention that preceded it,
  and the surprises are the most valuable thing in it.
- **Do not restate the roadmap here.** A second copy of "what is built" in a
  `CLAUDE.md` is how the first copy goes stale unnoticed. This file carries how
  to work in *this* repository; the roadmap carries what has been done across all
  of them.
- **A capability with no client path is not done — it is
  [owed](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md).**
  If you delete or fail to build a client path to a working service, add its row
  to that register in the same change.

### Decision records are append-only

An ADR is an account of what was decided and why, at a time. It is evidence, not
documentation, and its value is that it was not edited afterwards.

- **Never rewrite a record's body to match what was built.** Not to correct it,
  not to annotate it, not to add "as built, this differs". That pattern turns a
  record into a running commentary and destroys the thing it is for.
- **State changes in the `**Status:**` line, and nowhere else.** That is where a
  record says it is built, built in part (naming the part), or superseded —
  wholly ("Superseded by ADR N") or partly ("Partly superseded: X was reversed by
  ADR N; the rest stands").
- **A changed decision needs a new record that supersedes it.** If the code
  deliberately does something a record decided against, that is a decision and it
  is written down as one, with its own Context / Decision / Alternatives /
  Consequences. Both records then stand: the old one keeps its reasoning, the new
  one carries the change.
- **An unbuilt decision is not a superseded one.** "We have not done this yet"
  belongs in the Status line and the roadmap. Only a genuine reversal earns a new
  record.
- **Records live only in `architecture/docs/adr/`**, numbered sequentially in
  kebab-case. Adding one means adding it to `nav:` in `mkdocs.yml`, and
  `mkdocs build --strict` must pass.

**If the code and a record disagree, say so rather than quietly picking one.** An
honest "this is unresolved" is worth more than a plausible reconciliation that
reads as settled.

## Standing facts a new session needs

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
  it at a tagged version from the module proxy, no `replace`. A caller
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
