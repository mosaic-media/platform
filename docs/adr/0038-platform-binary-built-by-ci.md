# The Platform binary is built by CI; the Supervisor selects, not compiles

**Status:** Accepted; the CI release matrix is built (the producing half). A
version tag cross-compiles the one Platform binary to linux/amd64, linux/arm64,
darwin and windows with checksums, publishes a GitHub release, and builds a thin
multi-arch container image from the same bytes ([platform#50](0050-deployment-topologies.md)).
Unbuilt: signing the artefacts (waits on key custody, [platform#40](0040-module-distribution-and-trust.md)),
and the Supervisor that downloads, verifies and activates a release — the
consuming half — which does not exist. Core-module *selection* is built
separately (see the roadmap).
**Date:** 2026-07-22

Relocates [supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)'s build
sequence and supersedes [platform#4](0004-static-go-module-composition.md)'s
per-install Build Pipeline. Depends on
[architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md). Amends
[platform#23](0023-metadata-as-required-capability.md). Nothing here is built.

## Context

[supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md) has the Supervisor
prepare a workspace, resolve manifests, validate the dependency graph, edit a
`go.mod`, generate `imports.go`, run `go mod tidy` and `go build` — **on the
user's machine, at install time.** Follow that through to what a self-hosted user
experiences.

The Docker image must ship a Go toolchain. First boot needs network access to the
Go module proxy before Mosaic can serve anything at all. An arm64 NAS compiles the
entire dependency closure while the user watches a progress bar. A transient proxy
failure or a compilation error inside a third-party module is *the first thing a
new user sees*. Every module selection change repeats the whole thing. And every
install produces a binary that has never been built before anywhere, so a
compilation failure is discovered by the user rather than by us.

That is a compiler toolchain wearing an appliance's clothes, and
[supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md) opens by saying users should be
able to add Mosaic to a Docker Compose file and start.

The build pipeline was designed to solve a real problem: composing a tailored
Platform from a selected module set, deterministically, with atomic activation and
rollback. [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md) removes the reason it had to run
per install — third-party modules leave the binary entirely, so the only thing
being composed is a small, curated, first-party core set that is the same for
everyone.

## Decision

**Mosaic's CI builds and signs one Platform binary per release, with every core
module compiled in. The Supervisor downloads and activates it. Selection decides
which core modules are wired in, not which are present.**

### The build pipeline moves to CI

[supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)'s eleven-step
sequence is not deleted. Steps 1–9 — module selection, manifest resolution,
dependency-graph validation, SDK compatibility validation, workspace creation,
module download, `go.mod` update, generated `imports.go`, `go mod tidy`,
`go build` — **run in CI, once per release, over the core set**, on machines the
project controls. [platform#4](0004-static-go-module-composition.md)'s generated
`imports.go` survives exactly as specified; only its location and cadence change.

Steps 10 and 11 — pre-activation health checks and atomic activation — **stay
with the Supervisor**, because they are about the running system rather than
about compilation. So do everything else
[supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md) gives it: Generations, rollback,
recovery, the Shell, onboarding, diagnostics.

Both of [supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)'s prohibitions are preserved and one is strengthened. The
Supervisor still must not modify source repositories or the active Generation —
it now has no source to modify. It still must not analyse Go source to discover
module identity; manifests remain the non-executing source of truth, which
matters more once binaries are being installed rather than compiled
([platform#40](0040-module-distribution-and-trust.md)).

### Unselected core modules are present and disabled

The binary carries every core module. Selection decides what is constructed:

- **A core module the composition does not select is never constructed and never
  registered.** No `init` runs for it, no registry holds it, it cannot be resolved
  by role, and it cannot appear in a capability-gated affordance
  ([platform#24](0024-capability-gated-affordances.md)). It is code in the binary
  that never boots.
- **This reverses [platform#23](0023-metadata-as-required-capability.md)'s
  Supervisor-era paragraph**, which says an unselected module is "absent from the
  compiled Platform entirely — not compiled-in-and-disabled, just not there."
  Under a prebuilt binary it is precisely compiled-in-and-disabled, and that
  record's requirement must be re-expressed against the *selected* set rather
  than the compiled one.

### Role classes carry an arity and a mutability

A core module fills a **role class**. The class — not the module — declares how
many implementations may be selected and what changing the selection costs.

| Role class | Arity | Changing it | Why |
|---|---|---|---|
| Storage (`StorageAdapter`) | Exactly one | Destructive today; see below | Content does not move between engines without a migration that does not exist |
| Playback | One or more | New Generation | Local and remote are complementary; a deployment may want either or both |
| Metadata / search | One or more | New Generation | Required as a class ([platform#23](0023-metadata-as-required-capability.md)); satisfiable by core *or* extension modules |

**The table lives in Platform code, not in module manifests.** Arity is a property
of the role class, not of any module that fills it — a module has no business
asserting "I am the only one of me," and a manifest that could would be a manifest
that could lie. A core module's manifest declares only which class it fills; the
Platform validates a selection against the table above. This is honest about the
core set being closed and first-party: there is no third party who needs to
introduce a role class, so nothing is lost by not making it declarative.

Selection is **`Generation`-class configuration** in the existing reload-class
vocabulary. An admin changes it in settings; the Supervisor builds no candidate —
it activates a new Generation of the same binary with a different selection, and
rollback works exactly as it does for an upgrade.

### Changing the storage engine

**There is no migration today, and this record does not invent one.** What it does
do is decline to call the choice permanently irreversible, because two different
futures are being conflated:

- **SQL to SQL** — PostgreSQL to SQLite or back. A table-by-table transfer is
  plausible future work. The schema is Platform-owned and content-agnostic
  ([platform#9](0009-object-graph.md)), which is the property that would make it
  tractable. Not promised, not designed, not scheduled.
- **Relational to non-relational** — if a storage module ever implements
  `StorageAdapter` over something that is not a SQL database, a transfer is
  destructive by the nature of the change rather than by a gap in the tooling.

So the arity is *exactly one* and the mutability is *destructive today, possibly
migratable between SQL engines later*. Onboarding must present the choice as one
the user should get right, and must not imply a switch is coming.

**Factory reset does not exist** and this makes it necessary. What it destroys,
what it preserves (module settings? the admin user? telemetry?) and who can
trigger it are unanswered; it likely deserves its own record.

### Required capability classes

[platform#23](0023-metadata-as-required-capability.md)'s requirement is re-expressed:
the check runs over the **composed capability set — core and extension together**
— before the serve loop, and fails loudly when no provider fills `RoleSearch` and
`RoleMetadata`. A guarantee-clause core module ([architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md))
means that check passes on a fresh install with no configuration, which is what
[platform#23](0023-metadata-as-required-capability.md)'s seeded-Cinemeta stopgap was reaching for.

Runtime absence is a different matter and belongs with the boundary that
introduces it ([platform#39](0039-extension-module-boundary.md)).

## Alternatives considered

**Ship the build pipeline as designed.** *Rejected*, on the install experience
argued in Context. It is coherent, it is what is currently written down, and it
was the right design when third-party modules had to be in the binary. [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md)
removed that constraint.

**One binary per module *selection*, built by CI on demand.** *Rejected.* It
preserves true static composition and removes the user-side toolchain, but the
build matrix is the powerset of the core set, a selection change becomes a CI
round trip rather than a restart, and it would put Mosaic's CI in the path of
every user's configuration change.

**Keep the pipeline but pre-warm a module proxy cache in the image.** *Rejected.*
It shortens the compile without removing the toolchain, the build time, or the
failure mode; a build that fails on the user's machine is still a build that
fails on the user's machine.

**Omit unselected core modules using build tags, shipping variants.** *Rejected.*
It reintroduces the powerset problem in a different place, and the binary-size
saving is not worth a matrix of release artefacts that must each be tested.

**Let the Platform enable and disable core modules at runtime, no Generation.**
*Rejected.* Storage certainly cannot change under a running process, and a
playback provider appearing mid-session is a live-handover problem
([supervisor#4](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0004-supervisor-driven-live-handover.md)) that Generations already
solve. Using the mechanism that exists beats inventing a second one.

## Consequences

- **Installation becomes downloading a binary.** No toolchain in the image, no
  module proxy at first boot, no compile on a NAS, no build failure as a first-run
  experience. This is the largest practical gain in the whole thread.
- **The Supervisor gets smaller, which is what
  [supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md) asked for.** It loses build
  orchestration entirely and keeps installation, verification, Generations,
  activation, rollback, recovery and health — the things its dependability exists
  to protect. [supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)'s stated worry that build mechanics would make the
  Supervisor "larger and harder to recover" is resolved by removing them, not by
  delegating them.
- **Every released binary is pre-proven.** CI builds it once and every user runs
  the artefact that passed, rather than each install producing a first-of-its-kind
  build.
- **The Recovery UI's failure list shrinks.** [supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md) has it report manifest
  resolution, dependency validation, SDK compatibility, Go dependency, generated
  import and compilation failures. None of those can happen on a user's machine
  any more; download, signature verification, health check and activation failures
  remain.
- **The binary carries code it never runs.** Disabled core modules are size and
  latent surface. Acceptable for a curated first-party set; it would not be for
  an open one — which is the same reason
  [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md) closes that set.
- **Dependency conflicts between core modules become a CI problem.** They do not
  disappear. They become ours, discovered at release time, which is tractable only
  because the set is small.
- **Cross-compilation is now Mosaic's job for the Platform binary** — linux/amd64,
  linux/arm64, and whatever else is supported. Cheap for pure Go; the caveat is
  cgo, discussed with the same problem for modules in
  [platform#40](0040-module-distribution-and-trust.md).
- **The "single binary" vocabulary entry in `docs/index.md` needs revising.** It
  currently reads "the Platform Binary the Supervisor compiles Modules into
  ([platform#4](0004-static-go-module-composition.md))". The Supervisor no longer compiles anything.
