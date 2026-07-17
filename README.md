# Mosaic Platform

Mosaic is a home media center built around a single unified object model spanning movies, TV, anime, manga, comics, books, audiobooks, and music — one platform, one way of organizing, tracking, and presenting every kind of media a household collects, rather than a separate app per format.

## Source of Truth

This repository *implements* the Mosaic Platform. It does not define or redefine architecture, module contracts, or design decisions — those live in [`mosaic-architecture`](https://github.com/mosaic-media/mosaic-architecture), the canonical documentation repository for all Mosaic engineering guidelines (MEG), architecture canon (MAC), integration protocols (MIP), and design language/system (MDL/MDS) specifications.

When something here seems ambiguous or undocumented, the answer lives in `mosaic-architecture`, not in this repo. See `CLAUDE.md` for how Claude Code sessions are expected to use it.

## Quick Start

This repository is freshly initialized and does not yet contain a Go module or source tree — the scaffold is the next session's work (see `CLAUDE.md` → Current status). Once the scaffold lands:

```bash
go build ./...
go test ./...
```

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
