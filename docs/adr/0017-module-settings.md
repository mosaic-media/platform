# User-managed module settings

**Status:** Accepted
**Date:** 2026-07-20

## Context

The Stremio module ([platform#15](0015-module-capability-and-invocation.md), [platform#16](0016-optional-module-composition.md)) sources from Stremio addons a user chooses — a user pastes an addon's manifest URL to add it. The first cut wired those URLs through a `MOSAIC_STREMIO_ADDONS` environment variable read at composition time, a bridge that never matched the real need: a user adds an addon through the *running* API, not by editing the process environment and restarting.

Building the module surfaced the gap, which is the point of building modules — they are the forcing function that finds what the SDK is missing. The gap is **user-managed, module-scoped configuration**: a place a module's runtime settings live, a way a user sets them, and a way the module reads them on invocation.

This is deliberately *not* the platform Config system ([config versioning](https://github.com/mosaic-media/architecture/blob/main/docs/architecture.md)). That system is operator configuration — versioned, activated as a whole, and reload-classed (Hot/Restart/Generation/Recovery). You do not cut a new config Generation to add an addon. Module settings are user-owned data that changes freely at runtime, with no version to activate and no reload class to declare.

## Decision

**Module settings are one opaque JSON document per module, owned by a Platform store, set through a generic command, and handed to the module on each invocation.**

- **A Platform-owned store.** `ModuleSettingsStore` persists one settings
  document keyed by module id (a `module_settings` table, jsonb). It joins the
  `Tx` set, so a settings change and its outbox event commit in one transaction
  like every other write. A module owns no schema ([platform#8](0008-capabilities-do-not-own-stores.md)); its settings live in a Platform store it reads through the invocation, never a table it defines.
- **Opaque to the Platform.** The document is stored and returned uninterpreted;
  the module owns its meaning — [platform#9](0009-object-graph.md)'s unvalidated-JSON, writer-owns-correctness rule applied to configuration. The Stremio module reads `{"addons":[...]}`; the Platform never parses it.
- **A generic surface.** `configureModule(moduleId, settings)` and
  `moduleSettings(moduleId)` (commands and GraphQL) are generic across modules,
  because a module cannot contribute its own GraphQL without breaking the
  SDK-only boundary ([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md), [platform#15](0015-module-capability-and-invocation.md)) — the same reasoning that made `importContent` a generic Platform surface. `configureModule` refuses an id that names no registered module.
- **Handed to the module on invocation.** The SDK's `Capability.Import` takes an
  `ImportRequest{Caller, Query, Settings}` instead of a parameter list; the
  Platform reads the module's settings before invoking and passes them in. This
  is `v0.3.0`. The struct is the deliberate shape: what the Platform hands a
  module will keep growing as the module system matures, and a struct absorbs
  that without breaking the interface each time.

The `MOSAIC_STREMIO_ADDONS` bridge is **retired**. The Stremio module is now always registered and reads its addons from settings, so a user configures it at runtime.

## Alternatives considered

**Put module settings in the platform Config system.** *Rejected:* wrong semantics — operator-owned, versioned, reload-classed configuration, not free-changing user data. Adding an addon is not a config Generation.

**Let the module own a settings table.** *Rejected* by [platform#8](0008-capabilities-do-not-own-stores.md): a capability owns no schema. The Platform owns the store; the module reads through the invocation.

**Add a bare `settings []byte` parameter to `Import`.** *Rejected:* the next module gap (declared jobs, below) grows what a module receives again; a struct takes the churn once.

**Validate the settings document against a module-declared schema.** *Deferred:* the manifest does not yet carry a settings schema. Until it does, the document is unvalidated and the module owns correctness, consistent with [platform#9](0009-object-graph.md).

## Consequences

A user adds a Stremio addon through the served API and the next import uses it — no restart, no env var, no redeploy. The surface is generic, so the next module gets settings for free.

Two honest limits:

1. **Settings are opaque and unencrypted.** A module could store a secret (an addon URL with an embedded token) in plaintext jsonb. Reads are gated by a `module.read` action because of this, but real secret handling — a `secret://` reference resolved through the secret broker — is future work, not built here.
2. **This is the first of several "what the Platform hands a module" expansions.** The next is module-declared scheduled work (cleanup, periodic refresh), which needs the jobs runner, a scheduler, and — because such work runs with no user in the loop — the system principal [platform#13](0013-how-a-capability-acts.md) reserved. That is a larger slice, recorded here as the next gap the module surfaced.
