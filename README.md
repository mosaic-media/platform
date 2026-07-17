# Mosaic Platform

Mosaic is a home media center built around a single unified object model spanning movies, TV, anime, manga, comics, books, audiobooks, and music — one platform, one way of organizing, tracking, and presenting every kind of media a household collects, rather than a separate app per format.

## Source of Truth

This repository *implements* the Mosaic Platform. It does not define or redefine architecture, module contracts, or design decisions — those live in [`mosaic-architecture`](https://github.com/mosaic-media/mosaic-architecture), the canonical documentation repository for all Mosaic engineering guidelines (MEG), architecture canon (MAC), integration protocols (MIP), and design language/system (MDL/MDS) specifications.

When something here seems ambiguous or undocumented, the answer lives in `mosaic-architecture`, not in this repo. See `CLAUDE.md` for how Claude Code sessions are expected to use it.

## Quick Start

```bash
go build ./...
go test ./...
```

### Running the Platform against PostgreSQL

PostgreSQL is the mandatory first storage adapter (MEG-015 §05). The process
reads its connection string from `MOSAIC_POSTGRES_DSN`; when that variable is
unset the process still boots but skips storage bootstrap (a bridge until the
Configuration slice lands). When it is set, startup connects, runs schema
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
  mosaic-platform/
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
- **External module** (future) — product/domain capability packs, discovered at runtime, independently versioned. Not part of this repo's initial scaffold.

This three-tier layout is a correction to MEG-015 §02's original two-tier repository layout, made before implementation began. See `CLAUDE.md` for details and its outstanding documentation-sync status.
