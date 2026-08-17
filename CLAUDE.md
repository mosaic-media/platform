# Claude Instructions — Mosaic Platform

This repository is the Mosaic Platform: the binary a deployment runs, and the code
that decides what Mosaic is. If a document elsewhere disagrees with this source,
the document is wrong — fix it there, in the same session.

Fleet-wide conventions — commits, decision records, citation form, the roadmap —
are in [`architecture`](https://github.com/mosaic-media/architecture/blob/main/CLAUDE.md).
This file is what is specific to `platform`.

## Three package tiers, and dependencies point inward

- **Core Platform — `internal/platform/*`.** Domain, storage contracts, application
  services, policy, config, telemetry. Fully trusted, compiled in.
- **Built-in module — `internal/modules/*`.** Infrastructure implementing a Platform
  contract *in this process*, declaring itself through `internal/composition/builtin`
  in the shape an external module would, but compiled in and required. Postgres is
  the one that exists; it does not belong under `internal/adapters/`.
- **Extension module — not in this repository at all.** Installed at runtime and run
  out of process. `internal/adapters/extension` is the *host* of that tier, not a
  member of it: nothing above the capability registry knows it exists, because what
  the Platform holds is a `v1.Capability` either way.

`internal/adapters/` is for what is **not** module-shaped — helpers fulfilling one
small port — plus that host. Transports → application services → contracts and
domain; adapters and modules → contracts → external systems.
`internal/platform/domain` imports only the standard library.

**State the isolation tradeoff by tier, never the stronger of the two.** A core or
built-in module is linked in, so trust is established before the build; an extension
module has a process boundary, but its egress containment is a property of the
*deployment*, which is why `internal/adapters/extension/containment.go` reports a
posture rather than claiming enforcement.

**A transport calls application services and never opens a database.** Two packages
make that executable — `internal/transport/auth` and `internal/transport/health` —
each parsing every `.go` file's imports with `go/parser` and failing on
`internal/modules/postgres`, `github.com/jackc/pgx` or `database/sql`. **Both read
their own directory only and skip subdirectories**, so a new subpackage under either
goes unchecked, and no other transport carries the check at all.

**Author a screen with the generated `ui` builders, not `ui.Component`/`ui.Prop`.** A type
or prop spelled as a string is one nothing checks, and the props bag accepts it anyway — so
it draws nothing, with no error anywhere. Use the generic constructor only for what the spec
does not cover, then add it to the spec: a screen the vocabulary cannot express is a finding
for [`contracts`](https://github.com/mosaic-media/contracts), never a component or a CSS
rule added beside it in a client.

## The content model is a published module, not a package here

The content types and the content application-service API are **not** under
`internal/`. They are `github.com/mosaic-media/sdk`, required in `go.mod`, resolved from
the proxy with **no `replace`**, imported as
`v1 "github.com/mosaic-media/sdk/contracts/platform/v1"`; read `go.mod` for the version.
The store contracts and the identity, config and event models stay internal. **A
`replace` must never land in a commit.**

**Extension modules are not dependencies of this repository, and the test suite must
not import one either** — they are in neither `go.mod` nor the composition root. A
double stands in (`internal/modules/postgres/fake_capability_test.go`); a real one is
exercised by `.github/workflows/integration.yml`, not by the gate.

**The stop point is executable: if a capability needs a private Platform import, the
contracts are not ready to publish.** `capabilities/reference/boundary_test.go` refuses
an import there that is neither the SDK nor the standard library, and `test/sdkboundary`
compiles `test/sdkprobe` as a separate Go module, so Go itself refuses `internal/`.

## The command-handler boundary

Every command follows one order: validate the shape → authenticate → authorize →
open a `UnitOfWork` → load through contracts → apply domain rules → persist state
and outbox events in one transaction → return a Platform result type.

Steps 2 and 3 are `Service.enter` (`internal/platform/app/service.go`), which runs both
gates once and returns an unexported `authorized`. **A function taking an `authorized`
is inside the boundary; one taking a `v1.Caller` is an entry point.** Never call a public
`Service` method from inside a handler — that re-runs the whole boundary per result.

`internal/platform/app/boundary_conformance_test.go` enforces it: every caller-bearing
method is called with a session that was never issued and must answer `Unauthenticated`,
then with a real session holding no grants and must answer `PermissionDenied`; and a
reflection pass over `*app.Service` fails the build when such a method is in neither
`boundaryCases` nor `boundaryExempt`, or when a row or exemption names one that is gone.
That pass reads only a command struct's own fields, so a caller carried in an embedded
or nested struct is invisible to it.

**Work with no user runs as the system principal** (`internal/platform/app/system_principal.go`),
through this same boundary — never an invented caller, a skipped gate, or the session of
whoever triggered it. Its session reference comes from `crypto/rand` per process rather than
a sentinel, which a client could put on the wire and be handed unbounded authority.

## The store set, errors, config, printing

`Tx` (`internal/platform/contracts/unit_of_work.go`) is the Platform's store set: every
store reached through one `Tx` participates in the same underlying transaction, so state
and its outbox event commit atomically. The set is closed and enumerated in that one
interface, each accessor documenting why it joined — read the type, not a list here.
Capabilities own no stores, so nothing is registered or resolved at runtime, and growing
the set is an edit to a Platform interface.

Every contract error maps to one of the seven categories in
`internal/platform/contracts/errors.go`: a module may keep driver-specific errors
internally, but application services and transports only ever see a category. Every
configuration field declares a reload class (`internal/platform/config/reload_class.go`).

**Emit through `internal/platform/telemetry`; do not print.**
`test/logging/boundary_test.go` walks `internal/`, `cmd/` and `capabilities/`, refusing
the standard `log` package on the import alone and `fmt.Print*` / `fmt.Fprint*` calls,
resolving the `fmt` identifier through each file's own imports so an alias cannot slip
past. It fails if fewer than 100 files were scanned, so a renamed walk root cannot pass
by finding nothing.

## The gate

**Do not run `go build`, `go test`, `go vet`, `gofmt`, `go run` or a Mosaic binary on
the host.** One command:

```bash
docker compose -f docker-compose.test.yml run --rm test
```

It runs the record-index check, the citation lint, `licenseheader -check`, gofmt,
`go vet ./...`, `go build ./...`, `go test ./...`, then `go vet -tags mosaicdev ./...` and
`go test -tags mosaicdev ./internal/adapters/extension/...`. Append `bash` for a shell in
the same environment. `.github/workflows/verify.yml` runs **this compose file**, not a
transcription of it, in its `gate` job; its `checks` job repeats the cheap steps first.

**Two test dependencies fail soft, which is why the container is not optional:**

- **PostgreSQL.** `internal/modules/postgres`'s `TestMain` starts an *embedded* PostgreSQL
  when `MOSAIC_TEST_POSTGRES_DSN` is unset; when that fails, every test calling
  `requirePostgres` skips with a reason and the package still prints `ok`. The embedded
  server refuses to run as root — which the container is — so the compose file points
  the DSN at a throwaway `postgres` service, and that is what turns the skips into tests.
- **ffprobe.** `playback.NewProber` returns an unavailable prober when ffprobe is
  absent, and probing then degrades rather than failing — a behaviour change, not an
  error. `Dockerfile.dev`, which the test service is built from, carries ffmpeg for
  exactly this.

`test/sdkboundary` also skips outright when `go` is not on `PATH`, and needs the
sibling `../sdk` checkout the compose file mounts read-only where
`test/sdkprobe/go.mod`'s `replace` resolves to.

**Every Go file carries the SPDX header.** Add it with the tool in the container
(`go run ./tools/licenseheader`), never by hand, and change the text only in that
tool's `header` const.

`docs/adr/README.md` is the generated index — read it rather than counting files, and never
hand-edit it. It and the citation lint come from `scripts/adr_index.py` and `adr_lint.py`,
**vendored** from [`architecture`](https://github.com/mosaic-media/architecture): this gate
runs them, but a change to either belongs there.

## Running it

```bash
docker compose -f docker-compose.dev.yml up
```

Each compose file documents its own environment — read it rather than a copy. A fresh
stack seeds no administrator and starts at the setup wizard. Adding
`-f docker-compose.local.yml` after the first `-f` builds against the sibling working
copies: it writes a `go.work` inside the container only, so switching changes no committed
file, and it stands up a local signed module registry whose `registry-build` service
rebuilds the index after an extension module is edited. **A rebuilt index does not reach an
already-installed module** — uninstall and reinstall it from the extensions surface. Boot
re-adopts the pinned bytes from disk rather than following a catalogue that moved, which is
the pin working rather than a gap.

That registry exists **only in a `-tags mosaicdev` build**: in a shipped binary the
mechanism is absent rather than switched off — `devregistry_off.go` reads nothing and can
return no override — while every check on the install path is the same code either side.
