# One origin facility, which each consumer declares against

**Status:** Accepted. **Not built.** Prerequisite for
[platform#91](0091-module-served-resources.md), and the place a gateway origin
should land rather than becoming a fifth implementation.

## Context

The Platform serves bytes from its own origin in two places, built independently
and for different reasons.

[platform#20](0020-artwork-proxy-and-cache.md) is the artwork proxy. Module
metadata carries poster and backdrop URLs on third-party CDNs, and serving those
to a client directly is fragile: a CDN without CORS headers breaks the artlight
canvas and can break the image entirely, and it leaks the viewer's IP to the CDN.
It **signs** a URL it can reconstruct. It is about 250 lines.

[platform#25](0025-playback-consumer-and-media-origin.md) is the media origin.
The module never speaks HTTP; the Platform mints a ticket and serves
`/playback/{ticket}` itself. The ticket is **sealed**, not signed, because a
resolved location can be a credential and must never reach a client in readable
form. With probing, the per-stream decision, remuxing, range handling and
[platform#82](0082-the-origin-relays-or-serves-a-nominal-segment-grid.md)'s
nominal segment grid, it is about 6,500 lines.

Module-served resources would be a third, and a gateway
would be a fourth. Four implementations of *mint a token, bind it, expire it,
verify it, refuse it, cache the answer, re-serve it from our own origin* is four
places to fix one bug in, and three of them will not get fixed.

**The size difference is the important measurement.** Playback is not an origin
with different settings. It is a media pipeline that has an origin. Treating the
two as instances of one thing is what produces an abstraction that fits neither.

## Decision

**The Platform grows one origin facility, and a consumer declares against it.** A
declaration names: the token discipline (**signed** where the reference is
Platform-side identity, **sealed** where the reference is itself a secret), what
the token is bound to, its TTL, the cache policy, and the upstream the facility
fetches from when the token verifies.

**The facility owns only what all consumers demonstrably do**: the URL space,
minting, binding, expiry, verification, refusal, cache lookup and storage, and
re-serving from the Platform's own origin. It owns nothing else. **If it ever
grows a parameter only one consumer uses, that is the signal it has gone too
far**, and the answer is to give that consumer a handler rather than the facility
a knob.

**Both token disciplines stay, because both reasons stay.** Signing is right when
the reference is an identity the Platform can reconstruct and there is value in a
URL that is cacheable and legible in a trace. Sealing is right when the reference
is a credential. Collapsing them to one would either leak resolved locations or
make every artwork URL opaque.

**A token binds to the session id, not to the credential.** A reconnecting client
resumes the same session — it re-declares its route and replays from its last
`seq` — while [platform#58](0058-the-session-credential-is-a-bearer-pair.md)
rotates the credential underneath. Binding to the id therefore survives a
reconnect and still dies when the session genuinely ends, at sign-out, revocation
or idle expiry. Binding to the credential would break a long read on every
rotation.

**Artwork migrates onto it wholesale.** At around 250 lines it is small enough
that leaving it bespoke would be a deliberate second implementation of the thing
being extracted.

**Playback uses it for the token and keeps its pipeline.** Mint, bind, expire,
verify and refuse move to the facility; probing, the per-stream decision,
remuxing, range handling and the segment grid do not. That line is drawn here
rather than discovered later, because moving them would be the overreach this
record exists to avoid.

**A gateway is a consumer of this facility, not a new origin.** Slice 8 has
somewhere to land.

## Alternatives considered

**Migrating all three, playback included.** One origin, nothing left bespoke to
drift. Rejected: it pulls remuxing, range handling and the segment grid toward a
generic facility, and an abstraction that has to accommodate those fits none of
its other users.

**Leaving each origin bespoke.** Each stays shaped exactly for its job, and no
structural change blocks the module-resources slice. Rejected on the count: the
same five concerns would exist in four places, and the fix applied to one is the
fix nobody applies to the rest.

**One token discipline for everything.** Simpler to explain. Rejected because the
two reasons are both real and unrelated: one is about legibility and caching, the
other about not handing a client a credential.

**A module-facing origin API.** Rejected without much difficulty — it is the
same shape [platform#25](0025-playback-consumer-and-media-origin.md) already
refuses. A module never speaks HTTP, and a facility that let one would undo that.

## Consequences

- **Two kinds of consumer exist from the start** — one that migrates wholesale
  and one that takes the token half only. That asymmetry is the design, but it
  means "uses the origin facility" says less about a consumer than it appears to.
- **The cache becomes shared infrastructure.** One eviction policy, one size
  bound and one invalidation path now serve artwork, module output and whatever
  comes next, so a cache bug is a bug in all of them at once.
- **The signing key becomes shared.** What was per-origin key handling is now one
  key with one rotation story, which is better, and also a single thing whose
  compromise is wider.
- **Migration is a behaviour-preserving change to a path with no gate of its
  own.** Artwork's proxy is exercised by tests; the token discipline it shares
  with playback after this is not, and nothing currently compares the two.
