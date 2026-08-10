# The session credential is a bearer pair every client can hold

**Status:** Accepted and built in part (M0.3): the pair, rotation, reuse
detection, the device binding, idle expiry inside absolute expiry, per-device
revocation and the device list all landed, and the web client stores the pair in
`localStorage`. **Native keystore storage did not** — there is no native client
yet. Extends
[platform#43](0043-one-principal-many-credentials.md), which decides how a
credential resolves to a principal and says nothing about how a session is
carried or renewed.
**Date:** 2026-07-26

## Context

A session today is a fixed 24-hour lifetime with no renewal. `sessions.Manager`
has `Issue`, `Validate` and `Revoke`; the register lists `refreshSession` under
*never worked*. The Shell holds the session id in memory and re-authenticates on
boot from credentials compiled into the bundle, so a page reload is a fresh
sign-in and an absence of any length is a certain sign-out.

The release requires the opposite: come back after weeks and still be signed in.

The obvious web answer is an `HttpOnly`, `SameSite` cookie. It is genuinely the
strongest option against script access, and it is a **browser** mechanism. It
requires the client and the server to share an origin, it brings a CSRF surface
and the mitigations that go with it, and it is at best awkward for three of the
four clients the transport was chosen against
([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)) — Flutter, Compose and
native iOS all hold credentials in a platform keystore and send them explicitly.
Choosing it means the security-critical path has a variant per platform, and the
one that is exercised daily is not the one three clients use.

## Decision

**A session is a pair of opaque tokens. A short-lived access token is sent on
every call in a header; a long-lived refresh token is exchanged for a new pair.
Every client stores both in whatever secure storage its platform has, and the
transport treats every client identically.**

- **The access token is minutes long and opaque.** Not a JWT — for
  [platform#43](0043-one-principal-many-credentials.md)'s reason and not for
  fashion: a claims-carrying token makes a tightened limit or a revoked grant
  take effect only when the token expires. Validation is a store read, and that
  cost is accepted deliberately.
- **The refresh token is long-lived, bound to the device id the session already
  carries, and rotated on every use.** Rotation is the load-bearing part.
- **A refresh token presented twice revokes the whole chain.** Reuse detection
  is what makes theft *detectable* rather than silent; without it a long-lived
  credential is simply a long-lived credential.
- **Idle expiry sits inside absolute expiry.** `Session.LastSeenAt` already
  exists and is written; a session unused past an idle ceiling stops refreshing
  even though its absolute lifetime has not run out.
- **Revocation is per device and the user can see the list.** This is the
  affordance that makes a bearer pair defensible rather than merely convenient,
  and it is why the device id has been on the session since the beginning.
- **Storage is `localStorage` on the web and the keychain or keystore on
  native.** Stated plainly rather than smoothed over: **on the web, this is
  reachable by any script that gets into the page.** What limits the damage is
  the short access lifetime, rotation with reuse detection, and per-device
  revocation — not a belief that a token in a browser is safe. The Shell ships
  no third-party script and authors no inline markup, which keeps the exposure
  small, and that property is now security-relevant rather than only tidy.
- **Nothing is carried in a cookie**, so there is no CSRF surface and no
  same-origin requirement between the Shell and the Platform. The Supervisor's
  front door will make them same-origin anyway; the credential must not depend
  on it.

## Alternatives considered

**`HttpOnly` cookie plus a CSRF token.** *Rejected.* The better web answer and
the worse product answer. It is a per-client mechanism in the one place a
project can least afford variants, and it couples the credential to a front door
that does not exist yet.

**Raise the session lifetime and keep a single non-rotating id.** *Rejected.* A
stolen id is then valid for months and nothing anywhere notices. The renewal is
not the point; the rotation is.

**JWT access tokens with short expiry.** *Rejected*, on
[platform#43](0043-one-principal-many-credentials.md)'s grounds. It also buys
nothing here: the Platform is the only validator, so there is no second party to
save a lookup for.

**A per-client choice — cookie on web, bearer elsewhere.** *Rejected.* Two
mechanisms means two threat models, two revocation paths and two sets of tests
for the same feature, and the divergence is invisible until one of them is
wrong.

## Consequences

- Every client implements refresh, including retry-on-`Unauthenticated`. It
  belongs in the shared client layer rather than in each application, or the
  four clients will diverge on the one path that must not.
- The Platform gains a refresh-token store — hashed, rotated, chained, per
  device — and the register's `refreshSession` row stops being *never worked*.
- A refresh token used by an attacker and then by the legitimate user (or the
  reverse) signs that user out of that device. That is the intended behaviour,
  and it must be explained where a user meets it rather than presented as a
  fault.
- Passkeys ([platform#43](0043-one-principal-many-credentials.md)) change what
  *mints* the pair. They do not change what the pair is, which is why they can
  land after this without reopening it.
- Sign-out has a meaning it did not have: revoke the refresh chain, not merely
  drop a value the client was holding.
