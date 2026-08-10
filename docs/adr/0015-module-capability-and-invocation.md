# The module capability and invocation contract

**Status:** Accepted
**Date:** 2026-07-20

## Context

The reference capability ([platform#8](0008-capabilities-do-not-own-stores.md), [platform#12](0012-published-contract-surface.md)) proved the *authoring* half of the extension story: a package can be written against the published SDK alone and drive `ContentService`. It left the other half unbuilt — nothing defined how the Platform *invokes* a module. The reference capability was called directly from a test; there was no surface by which a running Platform hands control to code it did not author.

[sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) always reserved "capability interfaces" and "module registration APIs" for the SDK, but [platform#12](0012-published-contract-surface.md) populated only the content services. A shape was needed for two things: how a module presents itself, and how the Platform runs it.

Two constraints bounded the design. A module may import **only** the SDK ([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)) — so it cannot contribute a GraphQL resolver or any other Platform transport, because that would require importing Platform internals. And a capability originates no authority ([platform#13](0013-how-a-capability-acts.md)) — it acts as the caller the Platform hands it.

## Decision

**The Platform invokes a registered capability through a generic command, handing it the Platform's own content service and forwarding the caller.**

The SDK (`v0.2.0`) gains the capability side of its contract:

- A `Capability` interface a module implements: `Manifest()` plus
  `Import(ctx, ContentService, Caller, query) (ImportResult, error)`. (The
  `Caller` and `query` were folded into an `ImportRequest` struct in `v0.3.0` —
  [platform#17](0017-module-settings.md) — so the interface can grow without
  breaking again.)
- A minimal `Manifest` — `ID`, `Version`, `Name`. It starts small and grows
  as the module system needs it (declared permissions, sourced media types);
  those are named future additions, not omissions.
- An SDK-owned `ImportResult`, so the Platform consumes an import's shape
  generically rather than through a module-private type.

The Platform gains the invocation surface:

- A `CapabilityRegistry` (`id -> Capability`), populated once at composition.
- An `ImportContent` command, following the command boundary up to
  authorization — validate, authenticate the caller, authorise the action
  `content.import` — then resolving the capability by id (`NotFound` if
  absent) and invoking `Import`, passing the Platform's `app.Service` as the
  `ContentService` and forwarding the original `Caller`.
- An `importContent` GraphQL mutation over that command.

`ImportContent` opens no `UnitOfWork` of its own: the capability's service
calls each open theirs, one transaction per write. `ImportContent` is a
Platform command, deliberately **not** part of the published `ContentService`
— a capability is invoked *by* it and never calls it, so a module cannot
recurse into the invocation surface.

## Alternatives considered

**A module contributes its own GraphQL (or other transport).** *Rejected:* a third party cannot import the Platform's transport packages without breaking the SDK-only boundary ([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)). The Platform owns the invocation surface and routes to the module.

**The capability owns a store and joins the transaction.** *Rejected* by [platform#8](0008-capabilities-do-not-own-stores.md): a capability owns no schema and calls application services.

**The capability returns a module-private result type.** *Rejected:* the Platform could not then project the outcome (to GraphQL, to a test) without importing the module. The result type belongs in the SDK.

**`Import` takes a structured query rather than a string.** *Deferred:* a single `query string` matches the reference capability's shape and is enough for the walking skeleton (the Stremio module parses `type/id` from it). A richer query is a later, additive change.

## Consequences

Invocation is uniform: one command runs any registered capability, and a module needs no Platform import to be run. The invoking user's authority governs throughout — because the Platform hands the capability its own `ContentService`, every write the module makes re-enters the command boundary and is authorised again as that user ([platform#13](0013-how-a-capability-acts.md)).

Import is **not atomic across the whole tree**: each service call commits its own transaction, so a capability that fails partway leaves its earlier writes in place. This is the same property that lets a capability search between writes, and it is why import is idempotent by re-resolving what already exists rather than by rolling back.

The `Manifest` is intentionally thin. The permissions a module declares, and the media types it sources (which the `media_types` registry, [platform#11](0011-open-and-closed-vocabularies.md), will need), are future growth of this type, decided when a module forces them.

## Implementation implications

The first module built against this is the Stremio addon-source module; [platform#16](0016-optional-module-composition.md) records how it is composed into the binary. The relation-read gap noted in the roadmap (`ListFrom`/`ListTo` on `ContentService`) is orthogonal to this contract and remains a separate, additive `v0.x` change.
