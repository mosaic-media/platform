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

```bash
go build ./...
go test ./...
```

### Running the Platform against PostgreSQL

PostgreSQL is the mandatory first storage adapter. The process
reads its connection string from `MOSAIC_POSTGRES_DSN`; when that variable is
unset the process still boots but skips storage bootstrap. When it is set,
startup connects, runs schema
migrations, and **fails fast** if the schema is missing, incompatible, or
partially applied — it will not run against a mismatched database.

```bash
docker compose up -d   # starts local PostgreSQL 16 (see docker-compose.yml)
export MOSAIC_POSTGRES_DSN="postgres://mosaic:mosaic@localhost:5432/mosaic?sslmode=disable"
go run ./cmd/mosaic-platform
```

### PostgreSQL for tests

The storage contract tests (`test/contract`, run by the Postgres module's
integration tests) execute against a **real** PostgreSQL instance. Two ways to
provide one:

- **Nothing to install (default).** When `MOSAIC_TEST_POSTGRES_DSN` is unset,
  the tests download and start an embedded PostgreSQL automatically for the
  duration of the test run. The first run downloads a PostgreSQL binary (cached
  under `~/.embedded-postgres-go`), so it needs network access once. If it
  cannot start, the integration tests skip with a clear reason rather than
  failing the suite.
- **Use your own database.** Start one with `docker compose up -d` and point
  the tests at it:

  ```bash
  export MOSAIC_TEST_POSTGRES_DSN="postgres://mosaic:mosaic@localhost:5432/mosaic?sslmode=disable"
  go test ./...
  ```

  The DSN's user must be able to `CREATE`/`DROP DATABASE` — the migration tests
  create throwaway databases and drop them on cleanup.

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
    graphql/
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

Every Go file carries an SPDX license header, applied by a tool rather than by hand. CI (`.github/workflows/verify.yml`) verifies it on every push and pull request, so a file without the header fails the check — the failure prints the one command to fix it. To have the header added automatically before each commit, enable the local hook once:

```bash
git config core.hooksPath .githooks
```

## License

The Mosaic Platform is licensed under the **GNU Affero General Public License, version 3** (see [`LICENSE`](LICENSE)), with a **Module Linking Exception** (see [`LICENSE-EXCEPTION`](LICENSE-EXCEPTION)).

The AGPL protects the Platform: a modified Platform offered over a network must make its source available. The linking exception keeps the module ecosystem open — a Module that interacts with the Platform solely through the [Mosaic SDK](https://github.com/mosaic-media/sdk) (Apache-2.0), including one compiled into a Platform binary, may be released under any license its author chooses. The exception frees the Module, not the Platform.

> The linking exception is adapted from the FSF's own established exceptions — the [GPL Classpath exception](https://www.gnu.org/software/classpath/license.html) (used by OpenJDK) and the [GCC Runtime Library Exception](https://www.gnu.org/licenses/gcc-exception-3.1.html). It states the copyright holders' intent to permit module linking. It has not been individually reviewed by a lawyer, which is normal for a project this size; the practical effect if any wording were imperfect is only that modules could be more encumbered than intended, not any liability for users.
