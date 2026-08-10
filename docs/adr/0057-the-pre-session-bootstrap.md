# The pre-session bootstrap carries its own vocabulary

**Status:** Accepted and built. M0.2 landed the RPC, the transitively-closed
definition subset, the skin and the negotiated tree; the **forms** landed in M1,
on the action lane [platform#61](0061-the-pre-session-action-lane.md) adds. A third
doorway state joined the two below: a server that cannot read its own accounts
says so, rather than drawing a sign-in form that refuses every attempt.
**Supersedes
[platform#53](0053-the-pre-session-tree.md)**, which decided that a locked door may
speak, was built, was withdrawn the same day, and named the gap it did not
answer: where a pre-session tree gets its vocabulary. Carries
[platform#54](0054-claiming-an-unclaimed-server.md)'s two doorway states unchanged.
**Date:** 2026-07-26

## Context

The sign-in and onboarding screens were built and withdrawn on the same day. The
Platform served exactly the right tree; the browser drew *"SignInPanel — not
registered in this Shell"*.

The cause is structural rather than a bug. Definitions and the token set are
**pushed on connect** ([contracts#4](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0004-server-delivered-definitions-and-skin.md)),
which is to say after a session exists, and the client deliberately bundles no
components at all ([contracts#7](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0007-components-are-authored-only-in-the-contract.md)).
So a client without a session does not have a *thin* vocabulary. It has none —
no components, and no skin either, which is the half the withdrawal did not
mention and which would have produced an unstyled doorway even had the
components resolved.

Three ways out were named and none chosen: the endpoint returns the definitions
it needs alongside the tree; the doorway is built from primitives only; or the
library becomes an unauthenticated fetch.

Two constraints narrow the choice, and both are older than the problem.

- **Four clients** ([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)) —
  web, Flutter, Compose, native iOS. Anything that leans on a browser's ability
  to fetch and evaluate something late is a web answer wearing a contract's
  clothes.
- **Negotiation exists** ([platform#52](0052-vocabulary-negotiation-and-deliberate-degradation.md)):
  a client declares its vocabulary version and supported sets on `Attach`, and
  the server emits only what is supported. A pre-session call makes no `Attach`,
  so the declaration has nowhere to travel unless this record gives it one.

## Decision

**One unauthenticated RPC on `AuthService` returns the skin, the definitions the
tree needs, and the tree, in a single response. A client renders nothing before
it arrives.**

`AuthService` is where it belongs: it is already the service of the calls made
*without* a session ([platform#37](0037-one-client-transport.md)).

Five things follow.

**The request carries the same vocabulary declaration `Attach` carries.**
Version plus supported primitive and action sets, so
[platform#52](0052-vocabulary-negotiation-and-deliberate-degradation.md) applies to
the doorway exactly as it applies to every screen after it — the server emits
only what the client declared, and an unknown type is a reported telemetry event
rather than a silent placeholder. No client profile is sent: nothing at a
doorway plays.

**The response carries the definition *subset*, transitively closed over the
tree, not the library.** This is the one payload an unauthenticated party can
enumerate, so it should describe a doorway and nothing else. It is also what
keeps the pre-session path cheap enough to be the first thing every client does.

**The server decides which tree**, unchanged from
[platform#54](0054-claiming-an-unclaimed-server.md): the setup tree while the
server is unclaimed, the sign-in tree once it is not. A doorway has two states
and the client is not told which; it is shown one.

**The same call answers a refused session.** When a session is rejected the
client re-issues the bootstrap and renders what comes back, so "signed out" and
"never signed in" are one path rather than two — and a server that has since
been re-claimed or upgraded serves a doorway the client has not seen before
without needing to know it changed.

**It does not vary on the identity attempted.** The response is the same whether
a username exists or not, and it is rate-limited, because it is the one surface
reachable before authentication.

## Alternatives considered

**A primitives-only doorway.** *Rejected.* It creates a second, weaker
vocabulary used by exactly one screen, which is the shape that drifts — the
client's bundled components drifted for the whole life of the project behind a
green build ([contracts#7](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0007-components-are-authored-only-in-the-contract.md)),
and a doorway is the surface least likely to be re-checked. It also makes the
first screen a user ever sees the one screen not built from the design system.

**An unauthenticated definitions fetch, separate from the tree.** *Rejected.*
Two round trips before first paint, and worse, it splits "the vocabulary arrives
before the render" into two mechanisms that can each succeed while the other
does not. That is precisely the failure being fixed: the tree was right and the
vocabulary was absent, and nothing anywhere reported a mismatch.

**Bundle a fallback library in the client.** *Rejected.* This is the second copy
[contracts#7](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0007-components-are-authored-only-in-the-contract.md) deleted, and it
would return with the same drift and the same silence.

**Serve the doorway from the Supervisor.** *Rejected.* The Supervisor answers
for a Platform that is *down* ([supervisor#5](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0005-the-supervisor-observes-independently.md));
a Platform that is up must serve its own doorway, or signing in acquires a
dependency on the component that record keeps deliberately thin. It would also
put the SDUI vocabulary in two binaries.

## Consequences

- A client's boot becomes: bootstrap → doorway → sign in or claim → `Attach` →
  the session pushes the full library and skin. Two vocabulary deliveries, the
  second authoritative, and the first a strict subset of it.
- Definitions are disclosed to unauthenticated callers. That is a disclosure of
  the doorway's component shapes and nothing else, and it must not be allowed to
  grow into the whole library out of convenience — the subset is the security
  property, not an optimisation.
- The pre-session payload is a natural cache target and is deliberately not
  cached in the first build; a stale doorway after a re-claim is a worse failure
  than a round trip.
- The verification rule from the withdrawal applies with full force: **a doorway
  that has not been rendered in a browser has not been verified.** The server
  half of this was already right once.
