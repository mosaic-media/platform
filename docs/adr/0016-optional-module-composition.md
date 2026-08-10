# Optional-module composition

**Status:** Accepted. Parts are a deliberate bridge until the Supervisor's Build Pipeline exists.
**Date:** 2026-07-20

## Context

[platform#15](0015-module-capability-and-invocation.md) settled how the Platform *invokes* a module. This records how an optional module *gets into the binary* in the first place.

The end state is already decided. [platform#4](0004-static-go-module-composition.md) says a module is an ordinary Go library implementing the SDK, and the **Supervisor** resolves selected module manifests and drives a **Build Pipeline** that prepares a temporary workspace, edits a temporary `go.mod`, generates a single `imports.go` of blank imports, and builds a statically linked binary. Each module's `init()` registers it with the SDK registry. None of that machinery exists yet — there is no Supervisor Build Pipeline and no generated `imports.go`.

Something has to compose the first optional module now, without inventing the Build Pipeline ahead of the Supervisor. The question is what shape that takes, and how to keep it honest about being a bridge.

An **official optional module** — authored by the Mosaic team but built exactly as a third party would build one — is the vehicle. "Official" describes only its authorship, not its shape; building it the third-party way is what proves the third-party way exists.

## Decision

**An optional module is its own Go module outside `internal/`, importing only the SDK, statically composed into the binary by explicit registration in the composition root.**

- **Its own Go module, its own repository.** The Stremio module is
  [`mosaic-module-stremio`](https://github.com/mosaic-media/mosaic-module-stremio),
  module path `github.com/mosaic-media/mosaic-module-stremio`, importing only
  the SDK — enforced by a boundary test and, because it is a separate module, by
  Go itself (an external module cannot import another module's `internal/`). It
  began nested at `modules/stremio/` while the composition shape settled, then
  moved to its own repository as a third-party module would be.
- **Explicit registration in the composition root.** `main.go` constructs the
  module and registers its `Capability` into the `app.CapabilityRegistry`
  ([platform#15](0015-module-capability-and-invocation.md)). The Platform requires
  the module as an ordinary tagged dependency (`v0.1.0`) from the module proxy.
- **This explicit registration is a bridge.** It stands in for [platform#4](0004-static-go-module-composition.md)'s Build-Pipeline-generated `imports.go` and `init()`-based selection, exactly as `EnsureAdmin` ([platform#14](0014-first-admin-bootstrap.md)) bridges Supervisor onboarding. The *mechanism* of how a module plugs in is what this defines; the Supervisor automating *which* modules is later.

This fixes three package kinds that are easy to confuse:

| Location | Kind | Trust / shape |
|---|---|---|
| `internal/modules/` | Built-in module (e.g. Postgres) | Compiled-in, required, fully trusted; not independently versioned |
| `modules/` | Optional external-shaped module (e.g. Stremio) | Its own Go module, SDK-only, optional, invoked through the registry |
| `capabilities/reference/` | A package *inside* the Platform module | The authoring/boundary proof, not its own module |

## Alternatives considered

**Put the module under `internal/`.** *Rejected:* it could not be its own Go module, and "imports only the SDK" could not be proven from inside the Platform module — the whole point is to build it the way a third party must.

**Build the Supervisor Build Pipeline now.** *Rejected:* it is premature and Supervisor-scoped ([supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md), [supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)). Defining the plug-in mechanism does not require automating module selection.

**Runtime plugin loading.** *Rejected* by [platform#4](0004-static-go-module-composition.md).

## Consequences

The composition-and-invocation path is real and published: the SDK (`v0.3.0`) and the Stremio module (`v0.1.0`) are tagged and public, so the Platform requires them from the module proxy with no `replace`, and a fresh clone builds without any sibling working tree. The module is always registered; the addons it sources from are user-managed settings set at runtime ([platform#17](0017-module-settings.md)), not composition-time config — so the module is available even before any addon is configured. (An earlier cut gated registration on a `MOSAIC_STREMIO_ADDONS` env var; [platform#17](0017-module-settings.md) retired it.)

One honest debt remains, a bridge rather than the end state:

- **The Platform depends on the module directly** (now via an ordinary `require`, no longer a `replace`). [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) says the Platform must not depend on modules; in the end state the Build Pipeline imports selected modules into a *temporary* workspace, not the Platform's own `go.mod`. Until that pipeline exists, the composition root importing the module is the stand-in.

When the Supervisor's Build Pipeline lands, it replaces the explicit registration and the direct dependency with generated `imports.go` and `init()` registration; this ADR's registration path is the seam it slots into.
