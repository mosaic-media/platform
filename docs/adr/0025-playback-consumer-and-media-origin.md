# The playback consumer contract and the Platform-hosted media origin

**Status:** Accepted (built for `Direct`). `RolePlayback`/`PlaybackProvider`,
the sealed session-bound ticket and the `GET /playback/{ticket}` origin are
built, and `module-remote-playback` resolves against them. **`Served` — the
`io.ReadSeekCloser` half — is declared nowhere and implemented by nothing**,
since the torrent engine that would produce one is unbuilt. The "ranges and
seeking come free" property below holds for a relayed stream and **not** for the
remuxed one [platform#27](0027-stream-selection-against-a-client-profile.md) and
[platform#29](0029-probing-and-the-per-stream-playback-decision.md) introduced;
those records own that consequence.
**Date:** 2026-07-22

## Context

[platform#24](0024-capability-gated-affordances.md) named the concept of a
**consumer capability** — a role that acts on the materialised library rather
than populating the virtual plane — and deliberately stopped there, leaving the
first concrete consumer as "the larger follow-on". This record is that follow-on.

The library today is inert in a specific, mechanical way. `Import` snapshots a
`RemoteLocation` Part whose `Ref` is a direct URL or a magnet URI
([platform#18](0018-virtual-and-materialized-content.md)); nothing resolves it, and
nothing serves bytes. The `Play` action exists in the SDUI vocabulary and the web
runtime answers it with a toast. A user can add a film and cannot watch it.

**Stremio is the reference, and the useful thing it shows is not the addon
protocol** — Mosaic already has that, complete
([module-stremio-addons#1](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0001-completing-the-stremio-source-surface.md)). It is the split
*around* the addon protocol. Stremio is three parts, not two: addons return
stream *descriptors*; a local **server** turns a descriptor into a seekable,
byte-range HTTP origin (torrent engine, remux, subtitle conversion, cache); and a
player consumes that origin. Mosaic has the first and neither of the others.
Reading the module contract as "addons" alone is what left the gap invisible.

Building the resolver surfaces a question the source roles never had to answer:
**a consumer produces bytes, and bytes need a transport.** Every source role is a
request/response DTO exchange. This one is not, and the SDK has no HTTP surface —
correctly, since a module contributing a route would break the SDK-only boundary
([platform#15](0015-module-capability-and-invocation.md)) and the Platform owns
transports ([platform#3](0003-platform-as-execution-kernel.md)).

Three further gaps in the published surface fall out of the same slice, and are
recorded here because this is what forced them:

- **A Part cannot be read back.** `ContentService` has `AttachContentPart` and no
  read for parts anywhere — `GetContentNodeResult` is `{Node, Children}`. A
  playback provider literally cannot see the `MediaLocation` it is asked to play.
  This is a hole independent of playback; playback is only what found it.
- **Cross-module resolution is theoretical.** The playback module needs streams
  and subtitles *from the Stremio module*.
  [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md) said the contract
  supports module-to-module use but only the Platform resolves through it.
- **Provider precedence** ([sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)'s
  open seam) becomes live the moment several streams are offered for one item.

## Decision

**A playback provider resolves a Part to playable bytes and never speaks HTTP.
The Platform owns the origin: it mints a ticket, hosts the URL space, and serves
ranges over a reader the module hands it. Streams and subtitles from other
modules are resolved *by the Platform* and passed into the request.**

**The role.** `RolePlayback`, backed by `PlaybackProvider` — the first consumer
entry in a vocabulary that has been source-only:

```go
type PlaybackProvider interface {
    // Resolve turns a Part's location into something playable.
    Resolve(ctx context.Context, req PlaybackRequest) (PlaybackResolution, error)
    // Open serves the bytes for a Served resolution. The Platform closes it.
    Open(ctx context.Context, req PlaybackOpenRequest) (PlaybackStream, error)
}
```

**Two resolution variants, one interface.** A resolution is discriminated:

- **`Direct`** — a URL (plus any headers the origin requires) the bytes can be
  fetched from. Covers an addon's plain `url` stream and a debrid service's
  resolved link.
- **`Served`** — the module will produce the bytes itself, named by a stream id
  the Platform hands back to `Open`. Covers a torrent engine, a subtitle
  transcode, and any future transform.

`PlaybackStream` is an `io.ReadSeekCloser` plus content type and size. It is
stdlib, and it is exactly what `http.ServeContent` consumes — so the Platform
gets `Range`, `206`, `If-Range` and seeking for free, and the module never
imports `net/http` for the serving path. The variants are a closed set today;
a `Transcoded` variant is the named place transcoding would land.

**The Platform hosts the origin.** `internal/transport/playback` serves
`GET /playback/{ticket}`: it validates a signed, short-lived ticket bound to the
session and the Part, then either proxies the `Direct` URL or `http.ServeContent`s
the `Served` reader. This is the [platform#20](0020-artwork-proxy-and-cache.md)
argument applied where it matters more — the client fetches from Mosaic's own
origin, so the viewer's IP never reaches a CDN and a debrid link carrying a token
never leaves the server. Direct-to-source is available as a deliberate
per-resolution opt-out for the local-network case, not the default.

**The relay's cost is one egress leg, not two.** Worth stating because the
opposite is an easy assumption: relaying is 1× ingress from the upstream and 1×
egress to the client, and egress is what a host meters — the same egress as
serving a local file. The saving from letting a client fetch the upstream
directly is real (egress approaches zero) but it is paid for in the debrid token
reaching the client, a CDN that sets no CORS headers, and the loss of any
server-side handling of those bytes. [remux](https://github.com/lostb1t/remux)
relays for the same reasons — its stream handler streams the upstream body
through and forwards the range headers, with no redirect anywhere.

**Cross-module resolution stays Platform-mediated.** The Platform resolves
`StreamProvider` and `SubtitlesProvider` through the `CapabilityRegistry` and
passes the results into `PlaybackRequest`. The playback module receives them as
data. This keeps [platform#13](0013-how-a-capability-acts.md)'s cross-module
authority seam **closed** — every call still carries the invoking user, and no
module holds a registry handle.

**Provider precedence is answered by client capability, then by the user.** With
several streams offered, the Platform ranks them against the calling client's
declared profile and picks the best playable candidate
([platform#27](0027-stream-selection-against-a-client-profile.md)); a source picker
presents the list so the user can override. That closes
[sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)'s precedence seam for
foreground playback, and background auto-play inherits the same ranking.

**Parts become readable.** `ContentService` grows a part read. The Platform also
passes the resolved `Part` into `PlaybackRequest`, so a provider stays pure and
does not need the graph read to do its job — but the read is added because
write-without-read is a defect in the published surface on its own terms.

**The first module implements the direct path only.** `module-remote-playback`
resolves an `http(s)` location, and resolves a magnet through a debrid service
(Real-Debrid / AllDebrid / Premiumize) configured through its own settings screen
([sdk#4](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0004-module-contributed-settings-ui.md)). This is Stremio's
mainstream real-world path and it proves the entire chain. **The torrent engine
is a named, deferred slice**, and until it lands a magnet Part that no debrid
service resolves is honestly unplayable rather than silently broken.

**Transcoding is out of scope**, named rather than omitted. An ffmpeg subsystem
would swallow this thread.

**Amended in the building — a container rewrite is a serving-side transform, and
it does not fit `Served`.** Stream-copy remux
([platform#27](0027-stream-selection-against-a-client-profile.md)) landed on the
Platform's origin rather than in a module, and that is the right place by this
record's own rule: a module resolves and never serves, so putting ffmpeg behind
a module would hand it the byte path this contract keeps away from it. Two
consequences worth recording plainly:

- **`Served` does not describe it.** That variant promises an
  `io.ReadSeekCloser`, and an ffmpeg pipe has neither an index nor a length. The
  remux path therefore answers `200` and never `206`, and reports
  `Accept-Ranges: none` rather than claiming ranges it cannot serve. **A remuxed
  stream cannot be seeked** — and that is why the pipe is the wrong output.
  [platform#29](0029-probing-and-the-per-stream-playback-decision.md) replaces it
  with HLS, where a seek is a segment request; this caveat expires with the
  fragmented-MP4-down-a-pipe path it describes. A pure copy of every stream still
  relays progressively and keeps byte-range seeking for free.
- **The origin is the natural home for recovery too.** Because it fetches
  upstream before writing anything to the response, it can re-resolve a dead
  link and continue without the client noticing
  ([platform#28](0028-resolution-cache-and-capability-classes.md)).

## Alternatives considered

**The module serves its own HTTP listener.** *Rejected.* Modules compile in and
*can* import `net/http` ([platform#4](0004-static-go-module-composition.md)), so
this is possible — but it puts a second port, its own auth, its own lifecycle and
its own CORS story outside the Platform's control, and it makes the URL a client
must fetch a module implementation detail. The Platform owns transports.

**An `io.Reader` rather than an `io.ReadSeeker`.** *Rejected.* Seeking is the
whole point: a user scrubs, and a torrent engine's value is precisely that it can
prioritise pieces at an arbitrary offset. A non-seekable reader forces the origin
to fake ranges by discarding bytes.

**One `Resolve` returning only a URL.** *Rejected.* It reads simpler and it
excludes the torrent engine, the subtitle transcode and every future transform —
i.e. it excludes the half of `stremio-server` that is the reason this module
exists. The discriminated result costs one type and keeps the door open.

**Give the playback module a registry handle so it resolves other modules
itself.** *Deferred*, which is what [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md) already reserved. It is the more
honest module-to-module story and it opens cross-module authority and precedence
at the same time as the first consumer. Two hard questions at once, for no
capability the mediated form lacks today.

**Hand the resolved URL to an external player (mpv, VLC, Infuse) and stop.**
*Rejected as the primary path.* It is a genuinely cheap slice, but it captures no
progress, so resume and continue-watching — the things that make the library feel
alive — cannot exist. Worth keeping later as an explicit affordance, not as the
answer.

## Consequences

- **The library stops being inert.** This is the slice after which a user can
  search, add, and watch — the bar [platform#24](0024-capability-gated-affordances.md)
  set for the affordance gate to open.
- **The role vocabulary is genuinely two-sided.** `Manifest.Provides` now mixes
  source and consumer roles, and `roleImplemented` grows its first sink case.
  [platform#24](0024-capability-gated-affordances.md)'s gate has something real to gate on.
- **The SDK grows a byte-shaped surface without growing a transport.** `io` in the
  contract, `net/http` on the Platform. This is the seam that would have been
  easiest to get wrong.
- **Two long-standing gaps close as a side effect** — the part read, and (with
  the continue-watching work in [platform#26](0026-playback-state-is-platform-owned.md))
  pressure on the relation read (`ListFrom`/`ListTo`) that has been open since the
  reference capability.
- **Module lifecycle and scratch storage are named and not built.** A torrent
  engine needs `Start`/`Stop` and a Platform-granted cache directory; the debrid
  path needs neither, so the surface waits for the slice that forces it rather
  than being guessed at now.
- **The system principal is still deferred, narrowly.** Every path here runs
  inside a user's session. Cache eviction and prefetch — background work with no
  user — arrive with the torrent engine, and that is where
  [platform#13](0013-how-a-capability-acts.md)'s reserved gap finally has to be
  answered.
- **A consumer module does not fit the naming scheme.**
  [platform#2](0002-module-storage-and-delivery-model.md)'s `module-<system>` names the
  upstream system a module consumes; a playback module consumes no system.
  `module-remote-playback` is the name, and [platform#2](0002-module-storage-and-delivery-model.md) gains a sentence for the
  consumer case.

## Implementation implications

SDK: `RolePlayback`, `PlaybackProvider`, the request/resolution/stream types, and
a part read on `ContentService`. Platform: a `ResolvePlayback` application service
(read the Part, resolve the provider by role, gather streams and subtitles from
the registry, mint the ticket), `internal/transport/playback` as the origin, and
`CapabilityRegistry.PlaybackProviders()` plus the consumer predicate [platform#24](0024-capability-gated-affordances.md)'s
gate reads. Module: `module-remote-playback` at `v0.1.0`, direct + debrid, with a
boundary test holding it to the SDK exactly as `module-stremio-addons` is held.
The client half is [web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md); the state half
is [platform#26](0026-playback-state-is-platform-owned.md).
