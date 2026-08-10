# How a capability acts

**Status:** Accepted and built. Refines [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md).
The deferred **system principal** was built in M0.1: it is a caller like any
other, minted per process, resolved before any session read and allowed by the
policy decision point. Module-granular authority stays deferred and unbuilt.
**Date:** 2026-07-19

## Context

The Platform's policy engine authorises a `Subject`, and a `Subject` is a user: a `UserID` and an `AuthStrength`. Every application command takes a `CallerSessionID`, authenticates it to a user, and authorises that user. There is no other principal.

A capability has no session. To source metadata, search existing content, create nodes and publish an event — the four things [platform#8](0008-capabilities-do-not-own-stores.md) says a capability does — it must act *as someone*, and today there is no answer to who.

[sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) assigns "permissions" to the SDK but does not say what a module principal is. The temptation is to build one now: a capability as its own `Subject`, with its own granted permissions. That is the module-permissions problem in full, and it drags in questions nothing is yet asking — who grants a capability its authority, whether an operator approves at install, what a grant even means when [platform#4](0004-static-go-module-composition.md) already establishes that the capability's code is trusted before the build.

## Decision

**A capability does not originate authority. It acts within a context the Platform supplies, and forwards that context to every Platform service it calls.**

The shape follows from [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)'s "capability interfaces": the Platform invokes the capability — a user searches for an anime, and the Platform calls the capability to source it — so the Platform, as caller, owns the context and the principal in it. The capability is a participant in an action already authorised, not the initiator of a new one.

For the reference capability the principal is **the invoking user**. The context carries an opaque `Caller` ([platform#12](0012-published-contract-surface.md)), which is the user's session reference; the capability passes it to `AddContentWork`, `RelateContent` and the rest, and each authorises the user exactly as it would for a request the user made directly. Nothing in the policy engine changes, and every node a capability creates is attributable to the person whose action created it.

### What is deliberately deferred

**A system principal for background work.** A capability doing work with no user in the loop — a scheduled metadata refresh — has no session to forward. When that case is built it needs a well-known system principal in the invocation context, which is a small addition to the `Caller` abstraction rather than a change to this decision. It is not built now because the reference capability's four actions all occur within a user-triggered flow, and a system principal is effectively unbounded authority (acceptable only because trust is established before the build, [platform#4](0004-static-go-module-composition.md)) that should not be minted before something needs it.

**Module-granular authority.** A capability that must be *more or differently* privileged than the user it acts for — permitted to do what the user cannot, or restricted from what the user can — needs a module principal and a granting model: manifest-declared permissions, operator approval, enforcement. That is the module-permissions problem [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) gestures at, and it gets **its own ADR when a capability forces it**. Inventing it now would mean answering "who grants what to which module" with no module that needs a grant, which is the failure mode that retired the previous specification corpus.

## Alternatives considered

**A capability is its own principal now.** Add a module variant to `Subject` and build the granting model. *Rejected:* it is the full module-permissions problem, and it must answer "who grants" with a real mechanism before anything requires one. It also complicates the published surface ([platform#12](0012-published-contract-surface.md)) with a module-principal type the reference capability does not need — the two decisions interlock, and acting-as-user keeps the surface free of it.

**A system principal now, used for everything.** Give the capability a single service identity and route all its work through it. *Rejected:* it detaches every created node from the user who caused it, discarding an audit trail that the acting-as-user model gets for free, and it grants unbounded authority as the default rather than the exception.

**Let the capability hold a service handle and act without a supplied context.** Simplest to wire. *Rejected:* it makes the capability the originator of authority, which is exactly what it must not be. A capability that can act with no principal is a capability that can act as anyone.

## Consequences

**The reference capability is unblocked with no new machinery.** It forwards the `Caller` it is given; the policy engine, the command order and the audit trail are untouched.

**Every capability action traces to a user.** Attribution is a property of the model rather than something bolted on, for as long as capabilities act within user-triggered flows.

**Background capabilities are a known, named gap.** The first one to need a no-user context is the trigger for the system-principal addition, recorded here so it is a deliberate step rather than a surprise.

**Module-granular authority stays a separate decision.** This ADR does not pre-empt it; it scopes it out, so that when a capability genuinely needs authority distinct from its user's, that ADR starts from a real requirement.

**The published surface stays free of a module principal.** Because the caller is always a user reference, [platform#12](0012-published-contract-surface.md)'s `Caller` is an opaque session reference and nothing more, which is the smallest thing that unblocks the work.
