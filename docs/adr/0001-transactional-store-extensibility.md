# Transactional store extensibility

**Status:** Superseded by [platform#8](0008-capabilities-do-not-own-stores.md)
**Date:** 2026-07-18

> **Superseded on 2026-07-19.** This record solves for a capability owning durable state it must commit atomically with the outbox. [platform#2](0002-module-storage-and-delivery-model.md) rules that case out — capabilities do not own storage or schema — so the premise below does not hold. Retained for the reasoning and the alternatives, which remain a useful record of why a store must never have to ask permission to join a transaction.

## Context

The Platform's transaction boundary was a closed interface enumerating Core Platform stores:

```go
type Tx interface {
    Users() UserStore
    Sessions() SessionStore
    Credentials() CredentialStore
    Permissions() PermissionStore
    Config() ConfigStore
    Outbox() EventOutbox
}
```

This is the single atomic write path. An application service opens a `UnitOfWork`, resolves stores from the transaction, and appends outbox events — all in one transaction. There is no other way to commit a store change atomically with its events.

The contradiction surfaced while building the reference capability: a capability that owns durable state it must commit atomically with the outbox **had no way to join the transaction** without Core Platform's `Tx` being edited on its behalf.

That collides with two accepted positions: that the Platform should require no modification to support a new capability, and that built-in and module-delivered capabilities are architectural equals. A capability needing Core Platform edited for it is not an equal.

## Decision

Move to uniform, port-based store resolution.

1. **`Tx` becomes an opaque transaction scope** with no accessor methods.
2. **Every store — Core Platform or capability — is resolved identically**, through a package-level generic function rather than a named method:
   ```go
   func Store[T any](tx Tx) (T, error)
   ```
   A package function rather than a method, because Go methods cannot take type parameters.
3. **Storage becomes a port.** A `StorageAdapter` provides the `UnitOfWork` and binds each resolved store to the live transaction, so the built-in PostgreSQL adapter can be replaced without changing a call site.
4. **The SDK exposes storage for use, not modification.** Capabilities persist through Platform-owned storage contracts. They do not define tables, modify Core Platform schema, or open parallel databases.

## Alternatives considered

Each option was weighed on type safety at call sites, atomicity risk, and blast radius across the already-built slices.

**Extension registry on `Tx`** — a generic `Extension(key string) any` capabilities register into at startup. Type safety is lost at the call site: `any` plus a cast, a stringly-typed key, and runtime failure if unregistered. Blast radius would have been zero, which made it tempting. *Rejected:* it is an access gate. A store must ask permission to join the transaction, which is precisely the ceremony this decision exists to remove.

**Per-capability `UnitOfWork`** — a capability gets its own unit of work wrapping the underlying transaction. *Rejected:* cannot guarantee atomicity across the capability's data and the shared outbox.

**Generic transactional handle** — *chosen.* The only option that removes the private path rather than decorating it. Blast radius is high: every command handler moves off `tx.Foo()` onto uniform resolution. Accepted because static composition makes it free of any runtime bridge.

## Consequences

**Preserved.** Type safety at call sites, with no assertion. Atomicity — one transaction, one storage adapter, no parallel database. Capability equality: core and capability stores are obtained identically, and no store holds a private path into the transaction.

**Cost.** Every `tx.Users()`-style call site moves to uniform resolution. The change is contained to how stores are *obtained*, not what they do. `StorageAdapter` and the transaction scope become part of the public contract surface.

**Verification.** The decision holds when the reference capability persists atomically with the outbox using only published contract packages and no private Platform imports, and when the storage adapter can be substituted without changing a call site.

## Implementation status

Reverted. `Store[T]` and its resolver reached code and have been removed; `Tx` keeps its named accessors. The `StorageAdapter` port introduced alongside them is retained, because engine replaceability is a separate and still-live concern.

See [platform#8](0008-capabilities-do-not-own-stores.md).
