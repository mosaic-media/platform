# Mosaic Platform

Mosaic is a home media center built around a single unified object model spanning movies, TV, anime, manga, comics, books, audiobooks, and music — one platform, one way of organizing, tracking, and presenting every kind of media a household collects, rather than a separate app per format.

## Source of Truth

**The code in this repository is authoritative.** It decides what Mosaic is.

[`architecture`](https://github.com/mosaic-media/architecture) describes it and records the decisions behind it — three pages and a numbered ADR series, published at
[mosaic-media.github.io/architecture](https://mosaic-media.github.io/architecture/) with a PDF of each page.

If the two disagree, the documentation is wrong. Fix it there rather than
working around it here.

> The previous specification library — MEG, MAC, MIP, MDL, MDS and the rest —
> was retired on 2026-07-19 after it grew to 200+ largely unvalidated documents
> and began producing wrong work. It survives only at git tag
> `pre-reset-2026-07-19` in the architecture repository. Do not cite or try to
> read those identifiers.

See `CLAUDE.md` for what is built, what is next, and the rules for working here.

## Quick Start

**Everything runs in a container; nothing is built or run on the host.** The
full gate — license headers, gofmt, `go vet`, `go build`, `go test` — is one
command, and it brings its own PostgreSQL and ffmpeg:

```bash
docker compose -f docker-compose.test.yml run --rm test
```

Append `bash` to that command for a shell in the same environment when
iterating on one package. See `CLAUDE.md` for *why* this is not optional here:
two of this repository's most important test dependencies (a real PostgreSQL,
and `ffprobe`) fail **soft** when absent — a host run passes while testing far
less than it appears to.

The container expects the sibling `sdk` checkout present at `../sdk` (mounted
read-only): `test/sdkboundary` compiles `test/sdkprobe` against it to prove the
published surface imports cleanly from outside (ADR 0016), and that nested
module's `go.mod` resolves the SDK through a relative `replace`.

### Running the Platform

PostgreSQL is the mandatory first storage adapter. The process reads its
connection string from `MOSAIC_POSTGRES_DSN`; when that variable is unset the
process still boots but skips storage bootstrap. When it is set, startup
connects, runs schema migrations, and **fails fast** if the schema is missing,
incompatible, or partially applied — it will not run against a mismatched
database.

The dev stack brings the Platform, its PostgreSQL and the web Shell up together
and wires those variables for you:

```bash
docker compose -f docker-compose.dev.yml up
```

Add `-f docker-compose.local.yml` after the first `-f` file to build against the
sibling working copies of `sdk`, `sdui` and the modules instead of their
published versions.

### PostgreSQL for tests

The storage contract tests (`test/contract`, run by the Postgres module's
integration tests) execute against a **real** PostgreSQL instance. The test
container above supplies one — a throwaway `postgres:18` service on the compose
network, with no host port and no persistent volume — and points
`MOSAIC_TEST_POSTGRES_DSN` at it. There is nothing to install and nothing to
set.

> Run outside that container and the tests fall back to an *embedded*
> PostgreSQL, which **refuses to run as root** and skips the storage tests with
> a reason few people read. That soft failure is the whole reason the tests are
> containerised — a green run that tested no storage code looks exactly like one
> that did. If you need a database that *survives* a run (to look at real data),
> `docker compose up -d` starts a persistent one — see `docker-compose.yml` —
> but that is for inspection, not for the test suite.

## Repository Structure

Platform code is organized into three tiers of trust and delivery (see `CLAUDE.md` for the full model):

```text
cmd/
  platform/
    main.go
internal/
  platform/            # Core Platform — domain, contracts, app services
    app/
    contracts/
    domain/
    runtime/
    policy/
    sessions/
    config/
    secrets/
    diagnostics/
  modules/             # Built-in modules — required infrastructure, Module-shaped, compiled in
    postgres/
  adapters/            # Non-module-shaped helpers only — not a home for Postgres
    filesystem/
    crypto/
  transport/
    auth/                # Connect AuthService — mints a session (ADR 0061)
    session/             # Connect SessionService — the two-lane live session (ADR 0041)
    screens/             # The SDUI emit-side (ADR 0029)
    artwork/             # The artwork proxy (ADR 0030)
    playback/            # The media origin (ADR 0045)
    rpc/                 # Shared Connect plumbing: error codes, telemetry seam
    health/
  composition/
    builtin/           # Registers built-in modules the same way an external Module would be discovered
contracts/
  platform/
    v1/
test/
  contract/
  integration/
  fixtures/
```

- **Core Platform** (`internal/platform/*`) — fully trusted, compiled in, defines the rules everything else follows.
- **Built-in module** (`internal/modules/*`) — required infrastructure (Postgres first) that satisfies Platform contracts through the same shape a future external Module would use, but compiled in and fully trusted.
- **Optional (external-shaped) module** — product/domain capability packs, each its **own Go module in its own repository**, importing only the SDK and composed into the binary (the first is [`module-stremio-addons`](https://github.com/mosaic-media/module-stremio-addons)). Runtime discovery and distribution are still future; the composition mechanism exists (ADR 0019–0021).

See `CLAUDE.md` for the full tier model and the current state of the build.

## Contributing

Every Go file carries an SPDX license header, applied by a tool rather than by hand. CI (`.github/workflows/verify.yml`) verifies it on every push and pull request, so a file without the header fails the check. The test container's gate runs the same check first, so a missing header fails locally before it ever reaches CI; to add it to new files, run the tool in the container:

```bash
docker compose -f docker-compose.test.yml run --rm test go run ./tools/licenseheader
```

The local pre-commit hook (`git config core.hooksPath .githooks`, once per clone) is the exception that still touches the host — it shells out to the same tool, so it needs a Go toolchain on `PATH`. Skip it if you have none and rely on the container's check instead.

## License

The Mosaic Platform is licensed under the **GNU Affero General Public License, version 3** (see [`LICENSE`](LICENSE)), with a **Module Linking Exception** (see [`LICENSE-EXCEPTION`](LICENSE-EXCEPTION)).

The AGPL protects the Platform: a modified Platform offered over a network must make its source available. The linking exception keeps the module ecosystem open — a Module that interacts with the Platform solely through the [Mosaic SDK](https://github.com/mosaic-media/sdk) (Apache-2.0), including one compiled into a Platform binary, may be released under any license its author chooses. The exception frees the Module, not the Platform.

> The linking exception is adapted from the FSF's own established exceptions — the [GPL Classpath exception](https://www.gnu.org/software/classpath/license.html) (used by OpenJDK) and the [GCC Runtime Library Exception](https://www.gnu.org/licenses/gcc-exception-3.1.html). It states the copyright holders' intent to permit module linking. It has not been individually reviewed by a lawyer, which is normal for a project this size; the practical effect if any wording were imperfect is only that modules could be more encumbered than intended, not any liability for users.
