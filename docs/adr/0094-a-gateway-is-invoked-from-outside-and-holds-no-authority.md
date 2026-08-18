# A gateway is invoked from outside, and holds no authority of its own

**Status:** Accepted. **Not built.** Depends on
[platform#90](0090-one-origin-facility-consumers-declare-against.md). Records a
bounded exception to
[platform#58](0058-the-session-credential-is-a-bearer-pair.md). Slice 8 of the
extension surface.

## Context

A Jellyfin-compatible or DLNA facade reaches televisions and phones Mosaic will
never write a client for, which is why this slice is the one with the highest
strategic return.

**It was first drafted as another provider role, and every question fought its
answer.** How does it authenticate, when identity is wholly Platform-owned? May
it read the library, when `ListLibrary` was deliberately kept off the SDK? What
does it receive, when a module never speaks HTTP?

The reason is that the module taxonomy sorts by what a module does with data —
**sources** bring content in, **actors** do things outside, **composers** derive
from what is there — and all three share something the taxonomy does not name:
**they are invoked by the Platform.** A gateway is invoked by the outside world.
That is a different axis, and a gateway is the first thing on the far side of it.

Once that is said, the rest follows. A gateway supplies no capability. It is a
new *address* for capability the Platform already has.

## Decision

**A gateway is its own kind, distinguished by invocation direction.** It is not a
fourth shape beside sources, actors and composers; it is the first module the
Platform does not call. Its surface is shaped for being called: no provider role,
no capability declaration, and no per-invocation `Caller` minted for it, because
the caller arrives from outside.

**It is not a new tier.** Tier is trust and delivery, and a gateway is installed,
signed, digest-verified and run out of process like any extension module. What
differs is its shape, not how it is obtained or trusted.

**A gateway holds no authority of its own.** It acts wholly as the authenticated
user, through the same application services with the same authorization. Whatever
a person can see through Mosaic's own client they see through the facade, and
nothing else is reachable. There is no library-read grant and no manifest-declared
permission, because there is nothing to grant: it re-addresses what already
exists.

**A gateway may expose no capability the Platform does not already have, and a
gap it hits is a Platform finding.** This is the same rule a screen already
follows — a screen the vocabulary cannot express is a finding for the contract,
never a CSS rule beside the screen that wanted it. A facade needing something
Mosaic has no concept of does not invent it; it reports it.

**Credentials are translated, never verified.** A gateway extracts whatever its
protocol carries — Basic auth, a form post, a token header — and hands it to the
Platform, which authenticates it against its own identity store. Jellyfin signs
in with a username and a password, which Mosaic already has, so nothing new is
invented. The gateway translates a *shape*. It never decides who somebody is,
never stores a credential, and never sees an identity it was not handed.

### The exception this forces, recorded as one

[platform#58](0058-the-session-credential-is-a-bearer-pair.md) rejected a
non-rotating credential, and was right: "a stolen id is then valid for months and
nothing anywhere notices. The renewal is not the point; the rotation is." **A
foreign client cannot rotate.** It takes an access token and sends it unchanged
for months, and no translation changes that.

**So what a gateway hands back is a Platform-held reference to a session, not the
session credential.** The Platform keeps the real bearer pair and advances it
itself, so rotation survives where it can actually happen. The reference is
scoped to one gateway and one device, cannot be presented to Mosaic's own
transports, appears in the account panel's device list, and is revocable there
like any other device.

**What that preserves is the blast radius, not the rotation.** The reference is
still a bearer token that works until it is revoked or expires. Saying "the
Platform rotates underneath" without this sentence would imply
[platform#58](0058-the-session-credential-is-a-bearer-pair.md)'s property
survived intact on this path. It does not.

### What a gateway receives

**A decoded request in; a response that may be unary or a stream of frames.** The
Platform owns the socket, the TLS, the timeouts and the parsing.
[platform#25](0025-playback-consumer-and-media-origin.md) holds unchanged: a
module still never speaks HTTP, it answers a request the Platform already parsed.
Byte ranges and large bodies are streamed frames. The harness already carries
this shape — go-plugin's `GRPCBroker` multiplexes additional connections in both
directions today, which is how a module calls back into `ContentService`.

**The Platform performs any protocol upgrade and then bridges a framed
bidirectional stream.** The module speaks messages; it does not speak WebSocket.

**Server-initiated protocols stay inside
[platform#87](0087-module-lifecycle-events-progress-and-schedules.md)'s rule that
a module never calls out.** A DLNA `SUBSCRIBE` is recorded as *Platform* state.
When content changes, the Platform sends the `NOTIFY`, asking the module only for
the body shape. The subscription is the Platform's; the module supplies the
translation.

**A gateway declares its path prefix in its manifest, and a collision refuses the
install.** A facade needs a specific prefix to be compatible at all — a Jellyfin
client expects Jellyfin's paths — so the module must be able to ask for one.
Platform-reserved prefixes are never available. Two facades for the same protocol
therefore cannot be installed together, which is a real limit rather than an edge
case.

**Gateway traffic gets no session transport, no push lane and no SDUI.** It is a
facade for clients that will never speak Mosaic's protocol, and giving it half of
one would produce a second, worse client surface that nothing measures against
the contract.

## Alternatives considered

**A gateway as a composer with an inbound address.** One module surface, and a
facade genuinely does derive a new experience from what is there. Rejected: a
composer is called by the Platform and a gateway is not, so every part of the
surface that assumes invocation direction would grow an exception. Naming the
axis is cheaper than exempting from it repeatedly.

**Manifest-declared grants for a gateway, including library read.** Rejected once
the re-addressing rule was stated: a gateway that can do something its user
cannot is no longer re-addressing, and the question of what else it might be
granted has no natural end.

**The module authenticating, mapping its protocol's credentials to a Mosaic
user.** A foreign client would sign in exactly as it expects. Rejected: it puts
credential verification in a module, and it would have made this slice wait for
the authentication providers in slice 9.

**A distinct non-rotating credential class.** Simplest, and the client holds
exactly what it thinks it holds. Rejected as precisely the thing
[platform#58](0058-the-session-credential-is-a-bearer-pair.md) refused, granted
to the surface most exposed to a LAN.

**The gateway holding the rotating pair and issuing its own handle.** Rotation
fully preserved and no exception needed. Rejected: the gateway would be storing
credentials, contradicting the rule that it holds no authority.

**Unary request and response only.** Rejected on what it cannot do: no byte
ranges, no upgrade, no eventing. A media facade that cannot serve a range request
is not a media facade.

**Raw bytes over the pipe.** Every protocol fits. Rejected because every HTTP
parsing bug becomes a module's, which is what owning the listener was for.

## Consequences

- **A bearer token that works until revoked exists on this path.** It is scoped,
  visible and revocable, and it is still weaker than what every other client
  holds. An install with a gateway is a different security posture from one
  without, and should be described that way.
- **A second module surface exists** — one for things the Platform calls, one for
  the thing that calls it. Two surfaces to document, version and keep honest, and
  the boundary tests each module repository carries assume the provider shape.
- **The Platform grows a subscription registry** for server-initiated protocols,
  which is Platform state whose lifetime is tied to a foreign client's idea of a
  subscription rather than to a session.
- **Two facades for one protocol cannot coexist.** Prefix ownership is exclusive
  and there is no arbitration to add later without breaking compatibility, which
  is the reason the prefix mattered.
- **A protocol whose framing the Platform does not model still does not fit.**
  Unusual trailers and HTTP/2 push are Platform releases, not module work.
