# The published contract surface

**Status:** Accepted. Refines [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md).
**Date:** 2026-07-19

## Context

[sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) settles that the SDK is the public contract language between Platform and Modules, and that it owns "canonical Platform models," "capability interfaces" and "permissions." It is directional. It does not say which of the Platform's many interfaces a capability actually depends on, nor how those leave the Platform's `internal/` tree, where Go forbids an external module from importing them.

Trying to promote the contracts exposed the gap. Every store contract signature references a domain type — `NodeStore.Create(ctx, domain.Node)` — and both `contracts` and `domain` live under `internal/`. Compiling a throwaway module against the Platform confirms the barrier:

```
use of internal package
  github.com/mosaic-media/mosaic-platform/internal/platform/domain not allowed
```

The roadmap framed the fix as "promote the proven contracts into `contracts/platform/v1`." Working through what a capability actually calls showed that framing to be wrong in a way that matters.

**A capability never touches the store contracts.** `NodeStore`, `PartStore`, `Tx`, `UnitOfWork`, `StorageAdapter`, `EventOutbox` are how the *Platform* talks to its storage engine. A capability neither implements nor calls them. What a capability calls is the **application-service API** — `AddContentWork`, `SearchContent`, `RelateContent` — the command and query services that already enforce validation, authentication, policy and the transaction. Promoting the store contracts would publish the Platform's plumbing as public API, which is precisely the "SDK as implementation library" [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) rejects.

## Decision

**The published surface is the application-service API and the content models it carries. The store contracts stay internal.**

Concretely, the public package (staged in-repo as `contracts/platform/v1`, extracted to the SDK repository in the extraction slice) contains:

- **The content command, query and result types** — `AddContentWorkCommand`/`Result`, `AddContentChildCommand`/`Result`, `AttachContentPartCommand`/`Result`, `RelateContentCommand`/`Result`, `BindContentSourceCommand`/`Result`, `ResolveContentBindingCommand`/`Result`, `SearchContentQuery`/`Result`, `FindContentByExternalIDQuery`/`Result`, `GetContentNodeQuery`/`Result`, and the `BindingResolution` enum.
- **A service interface** exposing those methods, so a capability holds an interface and not the concrete `*app.Service`.
- **The content models** the commands and results carry — `Node`, `Part`, `MediaLocation`, `Relation`, `SourceBinding`, their identifiers (`NodeID`, `PartID`, `RelationID`, `SourceBindingID`), their vocabularies (`NodeKind`, `MediaType`, `ContainerType`, `ItemType`, `NodeStatus`, `PartRole`, `LocationScheme`, `RelationType`, `RelationOrigin`, `MatchMethod`, `BindingStatus`) and the canonicalisation helper (`NormaliseTypeName`, `Node.Canonical`).
- **An opaque `Caller`** — a session reference a capability receives in its invocation context and forwards to every service it calls. It carries no identity internals; what it *means* is [platform#13](0013-how-a-capability-acts.md). It exists here because every command needs it and `domain.SessionID` cannot be published without publishing the identity model.

What stays internal, and is therefore never public API:

- **The store contracts** — `NodeStore`, `PartStore`, `RelationStore`, `SourceBindingStore`, `Tx`, `UnitOfWork`, `StorageAdapter`, `EventOutbox`, and the identity/config/credential stores. These are the Platform↔engine boundary.
- **The identity, configuration and credential models** — `User`, `Session`, `Credential`, `ConfigVersion`. A content capability has no business reading them, and the eventing surface (`Event`, the envelope, core events named by [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)) is published when a capability needs to emit or subscribe directly, not before.
- **The policy vocabulary** — `policy.Subject`, `policy.Action`. Authorisation is the Platform's to perform, not the capability's to name.
- **The Platform's implementation** — `internal/platform/app.Service`, which implements the published service interface.

### Single source of truth, moved not copied

The models and command types **move** to the public package; the internal packages import them back. There is one definition of `Node`, matching [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)'s "canonical Platform models." A parallel public type set with conversion at the boundary is rejected below, as is code generation.

The dependency direction is preserved: the public package becomes the innermost contract layer, and `internal/platform/app` depends inward on it. Transport and adapters continue to depend on `app`, which depends on the published contracts.

### Published models are data, not behaviour

A published model carries its fields, its read-only predicates (`Node.IsRoot`, `Node.Orphaned`, `Part.Local`, `SourceBinding.NeedsReview`) and canonicalisation. It does **not** carry state transitions: `SourceBinding.Confirm`, `SourceBinding.MoveTo`, `Node.MarkOrphaned` and `Node.MarkActive` move into `internal/platform/app`, because changing Platform state is a command a capability issues, never a method it calls on a value it happens to hold.

### Enforcement

A throwaway module compiled against the published package — the same probe that found the barrier — becomes a standing test. It builds if and only if the surface is importable and self-contained, so an accidental leak of an internal type into a public signature fails the build rather than being discovered by hand.

## Alternatives considered

**Move `domain` and all of `contracts` out of `internal/` wholesale.** The fastest thing that compiles. *Rejected:* it publishes `Tx`, `StorageAdapter` and every store port as public API — the implementation library [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) rejects — and exposes identity and configuration models a content capability should never see. It converts the whole internal surface into a compatibility obligation at a stroke.

**A parallel public DTO set with conversion at the boundary.** Maximal isolation of internal evolution. *Rejected:* it contradicts [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)'s "canonical models" by creating two definitions of every type, and it taxes every field with conversion code in both directions. The content models were designed as boundary types already — `domain`'s own charter is "value types crossing contract boundaries, never rows or driver types" — so duplicating them buys isolation that is not needed.

**Generate the public package from the internal contracts.** The `contracts/platform/v1` placeholder's own comment says "generated," so this was the original intent. *Rejected for now:* it builds and maintains codegen tooling before there is a second consumer to justify it, and it puts generated code in the repository. Moving a curated set by hand is smaller, and generation can be revisited if the surface grows enough to warrant it.

**Publish the store contracts as the surface**, per the roadmap's original wording. *Rejected:* it is the mistake this ADR exists to correct. A capability calls application services, not stores; the stores are Platform plumbing.

## Consequences

**The surface is small and deliberate.** A capability author's first sight of Mosaic is nine services and the content models, not the Platform's storage internals. This is the lightweight SDK [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) asks for.

**The store set can still grow without touching the public API.** Adding a Platform store is an edit to `Tx` ([platform#8](0008-capabilities-do-not-own-stores.md)), which is internal; it changes the public surface only if a new *service* is published.

**A capability cannot reach storage even by accident.** With `Tx` and the store contracts internal, there is no import path from a module to a store, so the "resolvers call services only" discipline extends to capabilities by construction rather than by review.

**The published models freeze into a compatibility surface.** Every exported field of `Node`, `Part`, `Relation` and `SourceBinding` becomes something breaking changes must be rare and deliberate about ([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)). This is the cost of a stable SDK and is paid deliberately.

**The eventing and identity surfaces remain unpublished** until a capability needs them, at which point each is its own decision rather than a side effect of this one.
