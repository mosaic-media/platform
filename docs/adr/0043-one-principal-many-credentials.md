# One principal, many credentials

**Status:** Accepted. Unbuilt apart from `domain.PasskeyCredential` and two store methods. The passkey branch gained an enrolment policy in [platform#78](0078-passkeys-are-an-optional-layer-on-a-public-origin.md) — optional, and only on an install with a public origin — which narrows where that credential kind applies without changing this record's decision. [platform#79](0079-totp-is-the-second-factor-that-works-everywhere.md) adds TOTP, which is deliberately *not* a fifth credential in the diagram below: it resolves to no Principal on its own and makes the password path two-step instead.
**Date:** 2026-07-23

## Context

Today there is exactly one way to authenticate: a username and password, over
the Connect `AuthService`, minting a `domain.Session` that the `SessionService`
spends ([platform#37](0037-one-client-transport.md)). The session is an opaque
server-side reference validated against `SessionStore`, and `v1.Caller` carries
nothing but that reference ([platform#13](0013-how-a-capability-acts.md)).

Four credential kinds are wanted:

- **Password** — as now.
- **Passkeys** — WebAuthn, for the main client.
- **API keys** — issued by a user for a developer port, each key granting a
  *subset* of that user's permissions, chosen at issue time.
- **Legacy adapters** — a Jellyfin-protocol surface so existing clients work.
  Jellyfin is HTTP Basic. Connecting through it must not bypass anything.

The open question was what happens *after* authentication: whether the resulting
credential is a session, a JWT, or a cookie, and whether any of it reaches the
policy engine.

## Decision

**Every credential resolves to one `Principal`, constructed in exactly one
place. Sessions stay opaque and server-side. No JWT.**

```
password  ─┐
passkey   ─┤
API key   ─┼──→ Principal{ UserID, Strength, Attenuation, Scope }
Jellyfin  ─┘         ▲
                only constructor
```

- **The Principal is stamped, never claimed.** One function builds it, from the
  credential presented, and fixes `Strength` and `Attenuation` from what the
  credential actually is rather than from anything the caller says. This is the
  argument [sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md) makes for
  `moduleSpan` being the only place module telemetry is installed — attribution
  the Platform stamps cannot be forged — and it applies here for a stronger
  reason.
- **`authorized` carries the Principal** ([platform#41](0041-authorization-is-carried-in-the-type.md)),
  resolved once per request.
- **`domain.AuthStrength` records how identity was proved.** The field exists and
  has only ever held `AuthStrengthPassword`. It gains `passkey`, `api_key` and
  `legacy_basic`.
- **Cookies for the browser, headers for everything else.** The Shell gets an
  `HttpOnly; Secure; SameSite=Lax` cookie; API keys, Jellyfin and any future
  native client use `Authorization`. The transport decides how a credential
  arrives; resolution is shared.

### API key attenuation

A key may only narrow what its owner can do:

```
effective = user_permissions ∩ key_permissions
```

**Never a union, and computed at check time rather than stored at issue time.**
Snapshotting the intersection when a key is minted lets keys outlive the grants
that justified them — a user loses `content.import` and a six-month-old key
keeps it. Live intersection is the entire security property.

This is the one part that reaches the engine. `policy.Engine.Authorize` resolves
permissions *from the subject's user id*:

```go
roles, err := e.permissions.RolesForUser(ctx, subject.UserID)
```

An attenuated key has a user id and must not receive that user's full set. So
`policy.Subject` grows an optional attenuation set and the engine intersects.
The `Engine` doc comment says extensions are "expected to extend `Authorize`'s
logic later, not change its signature" — this is that, and the signature holds.

### Legacy adapters are transports

A Jellyfin adapter speaks Jellyfin on the outside and calls application services
with a Caller on the inside. It has no notion of its own about who may do what.

**The invariant is already enforceable rather than promised.** The existing
"transports call services only" rule is checked by tests that parse import
declarations in `internal/transport/auth` and `internal/transport/health`. Adding
a legacy adapter package to that test turns "Jellyfin cannot bypass policy" into
a build failure.

**Basic auth cannot verify per request.** The password hasher is Argon2id
(deliberately expensive). Verifying it on every request is a self-inflicted
denial of service. The first request verifies and mints a session; the adapter
holds it against the credential for a bounded window. That constraint falls out
of a choice already made rather than being a new one.

## Alternatives considered

**JWTs carrying identity and claims.** *Rejected*, and the reason is user 4 in
[platform#42](0042-three-authorization-mechanisms.md). A token's only real benefit
is skipping the lookup, and Mosaic cannot skip it: `SignOut` and `RevokeSession`
exist, so validation would consult a revocation list anyway — a session with a
signing key to rotate. Worse, putting permissions or content scope in the token
makes authorization **stale by design**: an administrator tightening a child's
age limit would not take effect until the token expired. For a child-safety
control that is a defect, not a tradeoff. Stateless validation wins across
services you do not control; Mosaic is one process.

**Let each credential kind produce its own caller type.** *Rejected.* Revocation,
audit and expiry would each need a per-kind answer, and every application service
would learn how the caller signed in. One principal keeps `RevokeSession` as the
single place access ends.

**Store the effective permission set on the API key at issue time.** *Rejected* —
see above. It is the difference between attenuation and a permission grant with
extra steps.

**Give the Jellyfin adapter its own permission mapping.** *Rejected.* It is the
whole failure this record exists to prevent. A protocol adapter translating
Jellyfin's model onto Mosaic's is a second policy engine, and the second one is
always the one that is wrong.

## Consequences

- **Passkeys change nothing structurally.** WebAuthn is a different proof of
  identity; the output is the same session. Two real effects: `AuthService` grows
  from a one-shot `SignIn` to begin/finish for the challenge round trip — a
  transport shape change, not an engine one — and `AuthStrength` becomes
  meaningful, since a passkey is phishing-resistant and a password is not.
- **Step-up authentication becomes available and uses the last unused hook.** A
  `legacy_basic` session probably should not perform `user.create` or
  `config.activate`. Requiring a strength for an action needs it in
  `policy.PolicyContext` — the other parameter the engine currently accepts and
  discards.
- **`HttpOnly` matters more here than usual.** The SDUI runtime renders
  module-supplied strings, which [sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)
  already classifies as untrusted content from outside the trust boundary. An
  XSS in a renderer displaying third-party text hands over a readable token
  immediately; `HttpOnly` is the one store JavaScript cannot reach.
- **The Shell should be served same-origin.** The cookie is clean only then. A
  separate static host means `SameSite=None` plus CORS, a materially worse
  posture, and serving the Shell from the Platform avoids the category.
- **The cookie only matters at Attach.** `SessionService` is a long-lived
  two-lane stream ([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)); once
  established, the stream is the session and no credential is re-presented per
  message.
- **Legacy clients get scoping they cannot ask for or refuse.** Jellyfin has no
  concept of a `ContentScope` and will request "all films". It receives the
  scoped set, cannot tell, and cannot opt out — but only because
  [platform#42](0042-three-authorization-mechanisms.md) puts the predicate in the
  store query rather than in the screen builders. The same decision paying off in
  an unrelated direction is the strongest evidence available that it is the right
  one.
- **The blast radius is one function.** If every credential produces a Principal,
  the only thing between a legacy Basic-auth connection and administrator rights
  is that constructor stamping strength and attenuation correctly. That
  concentration is the point — it is one function to review rather than four
  paths to keep in agreement — but it should be read as the load-bearing code it
  is.
- **The system principal is still absent.** [platform#13](0013-how-a-capability-acts.md)
  names it: background work with no user has no Caller to forward. Nothing here
  supplies one, and the jobs runner will force it.

## Status of the parts

Nothing in this record is built. Password sign-in and opaque server-side sessions
exist and are unchanged. Passkeys, API keys, attenuation, the legacy adapter,
cookie transport and `AuthStrength` values beyond `password` do not exist.
