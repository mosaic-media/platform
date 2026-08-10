# Claude Instructions — Mosaic Platform

## Source of truth

**The code in this repository is authoritative.** It decides what Mosaic is.
The [`architecture`](https://github.com/mosaic-media/architecture) repository
holds the roadmap and the cross-cutting records; this repository holds the
decisions whose mechanism lives here. If code and a document disagree, the
document is wrong — fix it, in the same session, rather than working around it.

**Thirteen repositories, siblings on disk.** `../architecture`, `../supervisor`,
`../sdk`, `../contracts`, `../web`, `../registry`, and the six `../module-*`
checkouts. **Each one describes itself** — read its `README.md` and its
`CLAUDE.md` rather than a summary written here, which is exactly the kind of
sentence that rots without anything going red.

What this repository must know about them is in its own files, not in prose:

- **`go.mod` is the list of modules compiled into this binary**, core modules
  included. Read it for what is required and at what version; do not trust a
  version written in a document.
- **Extension modules are not dependencies of this repository at all**
  ([platform#51](docs/adr/0051-extension-installation-is-user-initiated-and-persistent.md)).
  They are installed at runtime and adopted by the extension Manager, so they
  appear in neither `go.mod` nor the composition root — **and the test suite
  must not import one either.** A fake stands in:
  `internal/modules/postgres/fake_capability_test.go`.
- **A `replace` must never land in a commit.** Use one for local cross-repo work,
  then tag, push, bump the require, and remove it.

Required reading, and it is short:

- **[Roadmap](https://github.com/mosaic-media/architecture/blob/main/docs/roadmap.md)** —
  where the build is, across every repository.
- **[Unreachable capability](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md)** —
  what the Platform can do that nobody can ask it to do. **Read it with the
  roadmap, not instead of it:** a slice marked done there means the capability
  landed, not that a user can reach it, and no build or test failure will ever
  tell you the difference.
- **[Architecture](https://github.com/mosaic-media/architecture/blob/main/docs/architecture.md)** —
  the package map and the standing test gates. Read before changing structure.
- **[`docs/adr/README.md`](docs/adr/README.md)** — the generated index of the
  records this repository owns. Read the index first; it is the bounded thing.

> **The MDL/MDS/MEG/MAC/MIP/MOP/MAD/MDP specification library no longer exists.**
> It grew past 200 unvalidated documents and produced concrete wrong work — a
> roadmap built against an abandoned storage model, and an invented module
> transport layer the architecture forbids. **Do not cite a retired identifier
> and do not try to read those paths.** If something you need is missing, say so
> rather than reconstructing it.

## Package tier model

Three tiers, and the third one is real now rather than planned.

1. **Core Platform** — `internal/platform/*`.
   Domain, contracts, application services. Fully trusted, compiled in, defines
   the rules everything else follows.

2. **Built-in module** — `internal/modules/*`.
   Infrastructure implementing a Platform contract *in this process*, using the
   same registration shape an external module would — but compiled in, required
   and fully trusted. Postgres is the example, at `internal/modules/postgres/`
   (**not** `internal/adapters/postgres/`), registered through
   `internal/composition/builtin/`.

3. **Extension module** — not in this repository at all.
   Its own repository, its own release, installed by a user at runtime
   ([platform#51](docs/adr/0051-extension-installation-is-user-initiated-and-persistent.md))
   and run **out of process** behind a harness
   ([platform#39](docs/adr/0039-extension-module-boundary.md)).
   `internal/adapters/extension/` is the *host* of that tier rather than a member
   of one, and nothing above the capability registry knows it exists: what the
   Platform holds is a `v1.Capability` either way.

`internal/adapters/` is for things that are **not** module-shaped — helpers that
implement no full contract surface (filesystem, crypto), plus the host above.
Do not put Postgres there.

**State the isolation tradeoff accurately; it differs by tier.** A core or
built-in module is linked into the binary, so there is no runtime boundary and
no in-process sandbox — trust is established before the build, not enforced at
runtime ([platform#4](docs/adr/0004-static-go-module-composition.md)). An
extension module has a process boundary, and its egress containment is a property
of the **deployment** rather than a guarantee the Platform can make on its own —
see `internal/adapters/extension/containment.go`, which reports the posture
instead of claiming enforcement uniformly. Never state the stronger of the two.

## Non-negotiable rules

- **Dependency direction**: dependencies point inward. Transport → application
  services → contracts/domain. Adapters and modules → contracts → external
  systems. Domain must never import transport, adapter or database packages.
  Application services may depend on Platform contracts, never on concrete
  Postgres (or other module) types.
- **Error categories**: every contract error maps to one of `InvalidArgument`,
  `Unauthenticated`, `PermissionDenied`, `NotFound`, `Conflict`, `Unavailable`,
  `Internal` (`internal/platform/contracts/errors.go`). A module may keep
  driver-specific errors internally; application services and transports must
  only ever see these categories.
- **Command handler order**: validate the command shape → authenticate the
  caller → authorize via policy → open a `UnitOfWork` → load state through
  contracts → apply domain rules → persist state and outbox events in the same
  transaction → return a Platform result type. **Steps 2 and 3 are
  `Service.enter`** ([platform#41](docs/adr/0041-authorization-is-carried-in-the-type.md)),
  which runs both gates once and returns an `authorized`. An internal helper
  takes that `authorized` and reads stores directly; only an entry point takes a
  `v1.Caller`. Calling a public `Service` method from inside a handler re-runs
  the whole boundary — that is what made one search cost ten
  authenticate-plus-authorize cycles. It is enforced, not merely documented:
  `internal/platform/app/boundary_conformance_test.go` asserts every
  caller-bearing method refuses an unknown session and an ungranted caller, and a
  reflection pass fails the build if a new one is added without a row.
- **Transports call services only.** A transport is a projection surface, not a
  persistence layer: a handler calls application command or query services and
  never opens a database connection or queries a module directly. Boundary tests
  in `internal/transport/auth` and `internal/transport/health` enforce it by
  parsing import declarations.
- **A new screen never needs a client change, and never a component written in a
  client.** Screens in `internal/transport/screens` are composed from the SDUI
  vocabulary that already exists, through the generated `ui` builders. **The
  vocabulary has to allow it.** If a screen cannot be expressed, that is a
  finding about the vocabulary with exactly two honest answers: a new
  **definition**, authored as data in `contracts` and served from
  `definitions.Library()` at no client cost, or a deliberate growth of the
  **native vocabulary** — a primitive, a style field, an action kind — specced in
  the contract so every client can implement it, with a client release and a
  roadmap entry. Never a bespoke component or CSS rule added to `web` beside the
  screen that wanted it; [`contracts`](https://github.com/mosaic-media/contracts/blob/main/CLAUDE.md)
  and [`web`](https://github.com/mosaic-media/web/blob/main/CLAUDE.md) carry that
  rule in full and what it cost when it was ignored.
- **Author with the generated builders, not `ui.Component` and `ui.Prop`.**
  `ui.ExtensionCard(name, ui.Summary(…))` is checked against the contract;
  `ui.Component("ExtensionCard", ui.Prop("summary", …))` is a string that
  compiles whatever you spell. Reach for the generic constructor only for a type
  the spec does not cover — and then add it to the spec. A prop nothing renders
  is the quiet version of the same mistake: `ui.Subtitle` on a `Stack` drew
  nothing for the whole life of the extensions screen, because a props bag
  accepts anything.
- **Config reload classes**: every configuration field declares one — `Hot`
  (applies without restart), `Restart`, `Generation` (needs the Supervisor to
  activate a new Generation), `Recovery`. Classify a new field before adding it.

## Transaction shape

`Tx` is the Platform's store set, and every store reached through one `Tx` writes
to the same database transaction — so state and its outbox event commit
atomically. That is the whole purpose of the type.

**The accessors are enumerated in one place and documented there**:
`internal/platform/contracts/unit_of_work.go`. Each one carries the record that
added it and why it had to join the set. Read the type rather than a list here,
which would be a second copy of a set that grows.

- **Capabilities do not own stores**
  ([platform#8](docs/adr/0008-capabilities-do-not-own-stores.md), superseding
  [platform#1](docs/adr/0001-transactional-store-extensibility.md)). A capability
  sources, searches and adds content through the generic content model; it owns
  no schema, so it has no store to register.
- **`Store[T]` and its resolver are gone** — a service locator with a runtime
  failure mode, solving a case that does not occur. Do not reintroduce one.
- **The `StorageAdapter` port stays.** Engine replaceability is a separate,
  still-live concern, and PostgreSQL remains a module rather than a privileged
  implementation.
- **Growing the set is deliberate Platform evolution and should look like it** —
  an edit to a Platform interface, not a plugin point.

## The published SDK is a separate module

This is the single most surprising thing for a new session. **The content models
and the content application-service API do not live under `internal/`.** They are
`github.com/mosaic-media/sdk`, published and required in `go.mod`, resolved from
the module proxy with **no `replace`**
([platform#12](docs/adr/0012-published-contract-surface.md)).

- Content types are imported as
  `v1 "github.com/mosaic-media/sdk/contracts/platform/v1"` — `v1.Node`,
  `v1.Part`, `v1.Relation`, `v1.SourceBinding`, their vocabularies, the content
  command/query/result types, `v1.ContentService` and the opaque `v1.Caller`.
- **Use the `v1` constants, not `domain`.** `NormaliseTypeName` and
  `Node.Canonical()` are on the `v1` types.
- What stays internal: the store contracts (`NodeStore`, `Tx`, `StorageAdapter`)
  and the identity/config/event models in `internal/platform/domain`.
- **Read `go.mod` for the version in use**, and the SDK's own `README.md` Status
  section for what a version contains. Changing the SDK means editing `../sdk`,
  tagging, pushing and bumping the require here — with a temporary `replace` for
  the local loop only.
- **The stop point governs every SDK change: if a capability needs a private
  Platform import, the contracts are not ready to publish.** It is executable —
  `capabilities/reference/` and `test/sdkprobe/` import only the SDK, and
  `test/sdkboundary` compiles the probe as an external module.
- **The Platform holds the implementations; the SDK says how a module interacts
  with it** ([sdk#10](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0010-the-sdk-carries-no-implementation.md)).
  Anything that constructs, configures or encodes belongs on this side: the
  OpenTelemetry wiring is `internal/platform/telemetry`, and this repository
  requires the SDK's host module for the host-facing half. A finding that is a
  *shape* — a type, a verb, naming no library — is an SDK bump; one that can only
  be closed by naming a library is a Platform change reached through a
  declarative surface. **Do not describe the SDK's dependency graph here; read
  its `go.mod`.**

## Background work has a principal

**Work with no user acts as the system principal** — `Service.SystemCaller()` in
`internal/platform/app/system_principal.go` — and it goes through the same
authenticate-and-authorize boundary as everything else. Do not invent a caller,
do not skip the gate, and do not borrow the session of whoever happened to
trigger it.

The runner, the scheduler and the schedules are built:
`internal/platform/jobs/` holds `Runner` and `Scheduler`, and
`internal/composition/jobs/` wires the Platform's own jobs to them. **A module
still cannot declare a job** — there is no surface for it in the SDK — so that
remains an open finding rather than something to simulate locally.

## Standing facts a new session needs

- **Content vocabularies are open text, canonicalised on write**
  ([platform#11](docs/adr/0011-open-and-closed-vocabularies.md)). The test for
  open-versus-closed is "does Platform code branch on it?" — `node_kind`,
  `part_role`, relation types, match methods and statuses are `CHECK`-constrained;
  `media_type`, `container_type`, `item_type` are not. Stores call
  `v1.Node.Canonical()`, a contract obligation, so `Anime Series` and
  `anime-series` are one type.
- **How a module is composed and invoked**
  ([platform#15](docs/adr/0015-module-capability-and-invocation.md),
  [platform#13](docs/adr/0013-how-a-capability-acts.md)). `main.go`'s
  `registerCapabilities` wires the **core** module capabilities compiled into
  this binary and nothing else; an extension module reaches the same registry
  through the Manager after a user installs it. A caller invokes a module through
  a command — `ImportContent` for the import path — which authorises, resolves
  the capability by id, and hands it the `app.Service` as its `ContentService`
  plus the caller, **so the module's own writes each re-authorise as the invoking
  user.**
- **Metadata and streams are independent.** A meta-only import yields Works and
  their tree with **no Parts**, and that is the correct outcome rather than a
  degraded one. A module that snapshots a remote stream writes a Part with
  `Scheme: v1.RemoteLocation`
  ([platform#10](docs/adr/0010-storage-authority-and-transaction-scope.md)'s
  remote path).
- **Module settings are user-managed, opaque JSON**
  ([platform#17](docs/adr/0017-module-settings.md)) — *not* the Platform config
  system, which is operator config with reload classes. `ModuleSettingsStore`
  holds one jsonb document per module id; the Platform stores it uninterpreted
  and hands it back on invocation. The module owns its meaning.
- **UUIDv7 for content ids.** `NewIDGenerator()` (UUIDv4) serves the
  infrastructure tables; `NewUUIDv7Generator()` serves the content tables.
  Content ids are native `uuid`; infrastructure ids stay `text`/UUIDv4 and are
  not migrated.
- **SQLSTATE `23001` → `Conflict`** (an explicit `ON DELETE RESTRICT`).
- **Password hashing is Argon2id** in `internal/adapters/crypto`, PHC-encoded.
- **Left unbuilt, not invented**
  ([platform#9](docs/adr/0009-object-graph.md)): the fractional ordering scheme
  at scale, relation confidence decay (edges are written once — `RelationStore`
  has no `Update`), and attribute validation (JSONB is unvalidated by design).

## Running the Platform

**In the dev stack, not with `go run`.** It brings its own PostgreSQL, its own
ffmpeg and the Shell, already wired together:

```bash
docker compose -f docker-compose.dev.yml up
```

`docker-compose.dev.yml` documents its own environment — read it rather than a
copy. Two things worth knowing before you look:

- **A fresh stack is unclaimed and starts at the setup wizard**
  ([platform#54](docs/adr/0054-claiming-an-unclaimed-server.md)). It seeds no
  administrator. The `MOSAIC_BOOTSTRAP_ADMIN_*` pair still exists for an
  automated deployment and as the way back into a dev box whose login was lost,
  but it is unset by default and setting it is a deliberate act.
- Add `-f docker-compose.local.yml` after the first `-f` to build against the
  sibling working copies instead of published versions. The overlay writes a
  `go.work` inside the container only, which is why switching changes no
  committed file.

### The local module registry ([platform#55](docs/adr/0055-the-development-module-repository.md))

Extension modules are not dependencies, so a local change to one reaches a
running Platform through the *install path* or not at all — and that path leads
to the official registry, whose URL and key are compiled in. The overlay
therefore stands up a local registry, signed with a throwaway key
(`tools/localregistry/assemble.sh`), and the Platform warns on every boot that it
is using one.

**Nothing is bypassed.** A development key signs a development index and every
check the real path runs still runs — index signature, manifests, SDK major,
binary digest, handshake. A loop that skipped verification would exercise a path
production does not have.

**Those variables exist only in a `-tags mosaicdev` build.** In a shipped binary
the mechanism is *absent*, not switched off — see
`internal/adapters/extension/devregistry_off.go`. Both configurations are gated.

After editing a module:

```bash
docker compose -f docker-compose.dev.yml -f docker-compose.local.yml run --rm registry-build
```

then **uninstall and reinstall it from the extensions surface.** A rebuilt index
does not reach an already-installed module: boot re-adopts the pinned bytes from
disk rather than following a catalogue that moved
([platform#51](docs/adr/0051-extension-installation-is-user-initiated-and-persistent.md)),
which is the pin working rather than a gap. Local builds are versioned
`local-<git describe>` so they cannot be mistaken for a release.

## The records this repository owns

`docs/adr/` holds the decisions whose *mechanism* is here — the composition root,
the store set, the transports, the extension host, the release workflow.
[`docs/adr/README.md`](docs/adr/README.md) is the generated index; it is the
bounded thing to read, and it is not edited by hand.

**The index generator and the citation lint are not vendored into this
repository yet, and nothing in this repository's gate runs them.** They live in
`architecture/scripts/`, and the index here was generated by them. Do not claim a
gate step that does not exist; if you regenerate or lint, say which script you
ran and from where.

<!-- shared-rules:begin -->
## Rules every Mosaic repository shares

*Generated. The source is `architecture/shared/repository-rules.md`; edit it there
and run `scripts/shared_rules.py --write` across the fleet. A copy edited in place
fails its repository's gate, which is the point: these rules were eleven
hand-kept copies in four variants, and the abridged ones had quietly dropped the
reasoning while keeping the rules — and in one case dropped a rule outright.*

### What this file may say

**A `CLAUDE.md` states rules, and facts about its own repository. It does not
state facts about another one — it links instead.**

An audit of all twelve of these files against their source found 74 stale claims.
None of roughly 180 rules was wrong; 62 of the 74 were facts about somebody
else's repository. Ownership predicts rot: a fact about this repository stays true
because whoever changes the code changes the sentence in the same session, and a
fact about another one dies the moment they edit it with nothing here going red.

The same applies to facts this repository already publishes in a generated
artefact — counts, versions, what is built. Point at the artefact.

### Decision records live with the code they govern

Each repository owns the records whose *mechanism* it holds — the spec file, the
lint gate, the conformance corpus, the composition root, the release workflow.
A decision can bind five repositories and still have exactly one steward.

- **`docs/adr/`**, numbered from 1 in every repository, with `docs/adr/README.md`
  a **generated** index. Read the index first; it is the bounded thing.
- **A record's heading carries no number.** The number lives in the filename and
  the index only, so a record's anchor survives being renumbered.
- **Cite a record as `repo#N`, and make it a link** — a relative path within a
  repository, an absolute URL across them, and the bare label only where no URL
  is possible, such as a code comment or a Dockerfile. The old `ADR NNNN`
  spelling is refused by a lint: once every repository numbers from 1, that form
  resolves quietly to a *different* record instead of dangling, and no tool in
  the fleet could detect it.
- **Cross-cutting records stay in [`architecture`](https://github.com/mosaic-media/architecture)** —
  the ones with no enforcing mechanism anywhere: licensing, repository naming and
  topology, the module tier model.

### Decision records are append-only

An ADR is an account of what was decided and why, at a time. It is evidence, not
documentation, and its value is that it was not edited afterwards.

- **Never rewrite a record's body** — not to correct it, not to annotate it, not
  to add "as built, this differs". That turns a record into a running commentary
  and destroys the thing it is for.
- **State changes go in the `**Status:**` line and nowhere else** — built, built
  in part (naming the part), or superseded, wholly or partly.
- **A changed decision earns a new record that supersedes it**, with its own
  Context / Decision / Alternatives / Consequences, and both records then point
  at each other through their Status lines. The old body stays exactly as it was.
- **An unbuilt decision is not a superseded one.** "Not done yet" belongs in the
  Status line and the roadmap; only a reversal earns a new record.

### The roadmap is maintained, not consulted

**`docs/roadmap.md` in [`architecture`](https://github.com/mosaic-media/architecture)
is the single record of where the build is, across every repository.** It stays
there because a milestone spans repositories by construction. Read it before
starting, and **update it in the same session as the change that dates it** — not
in a follow-up, which does not happen.

- A slice that lands is marked landed, **with what it left out named in the same
  sentence**. "Built" with no qualifier claims the whole slice shipped.
- Implementation that departed from its record is recorded where it departed.
  The surprises are the most valuable thing in it.
- **Do not restate the roadmap here.** A second copy of "what is built" in a
  `CLAUDE.md` is how the first copy goes stale unnoticed.
- A capability with no client path is not done — it is
  [owed](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md).

### Demonstrated, not asserted

**Say what you actually ran.** A skipped test is not a passed test, and "it should
work" is not evidence.

Each repository's container is the authority on its own gate, and the command is
in that repository's section below. It exists because the checks that matter fail
*soft*: a missing PostgreSQL skips storage tests and still prints `ok`, a missing
generator toolchain produces a drift guard that passes by not running. Where the
container cannot be run, running what you can on the host is better than running
nothing — **provided you report which checks ran and which did not.** Claiming a
gate passed when it was not executed is the one thing this rule exists to stop.

### Commit and push

- **Commit and push each repository separately.** They are siblings on disk and
  independent in git.
- **Commit author identity** must be `AdamNi-7080 <anicholls41@gmail.com>`. If git
  has no identity configured, set it repo-locally rather than globally.
- **Push once the change has been demonstrated working in this session.** Commit
  locally and say so otherwise. **Force-push always requires asking.**
<!-- shared-rules:end -->

## The gate

**Do not run `go build`, `go test`, `go vet`, `go run` or any Mosaic binary
directly on this machine.** Every gate runs inside the test container:

```bash
docker compose -f docker-compose.test.yml run --rm test
```

That is the whole gate — license headers, gofmt, `go vet`, `go build`,
`go test`, then a second `-tags mosaicdev` pass — and
`.github/workflows/verify.yml` runs this exact file rather than a transcription,
so what refuses a push is the thing you just ran. Append `bash` for a shell in
the same environment.

**The reason is that this repository's two most important test dependencies fail
soft**, letting a run pass while testing far less than it appears to:

- **PostgreSQL.** The contract tests in `internal/modules/postgres` skip, with a
  reason, when no database is reachable — so `go test ./...` on a host with none
  prints `ok` having exercised no storage code. The embedded fallback refuses to
  run as root, so the naive containerised version skips too; the compose file
  points `MOSAIC_TEST_POSTGRES_DSN` at a real service to convert those skips back
  into tests.
- **ffprobe.** Playback probing shells out to it
  ([platform#29](docs/adr/0029-probing-and-the-per-stream-playback-decision.md)).
  Absent, the Platform relays unprobed — a behaviour change rather than an error,
  and a release with undecodable audio then plays silently.

**Every Go file carries an SPDX header** (`AGPL-3.0-only`). Add it with the tool,
never by hand — `docker compose -f docker-compose.test.yml run --rm test go run
./tools/licenseheader` — and change the header text in one place, the `header`
const in that tool. CI checks it in seconds, ahead of the container job. A local
hook adds it before the commit: `git config core.hooksPath .githooks`, once per
clone.

## Working in this repository

- **Develop and commit directly on `main`.** This repository does not use feature
  branches for Platform implementation work.
- **Build one slice at a time, in roadmap order.** Do not start a slice whose
  prerequisites have not landed, and commit per passing slice rather than once
  for a whole sequence.
- Each slice passes the standing test gates before the next dependent slice
  begins. Nothing is declared done on a host-side build.
- **When ambiguity comes up, read the code first.** If neither the code nor a
  record answers it, say so — an honest gap is worth more than an invention that
  reads as settled.
