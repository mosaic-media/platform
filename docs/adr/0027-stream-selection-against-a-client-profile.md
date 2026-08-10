# Stream selection against a client profile

**Status:** Accepted (built, against a client-declared profile). Candidate
gathering, profile filtering and ranking are built inside `ResolvePlayback`,
and it reports what it chose out of how many. **The profile is now declared
rather than assumed** ([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)), and ranking gained an HDR term the record does
not anticipate: HDR a client cannot render is penalised, because rendering it
means a tone-map and a tone-map is a full video re-encode. **Unbuilt:** the
source picker, and the no-candidate state with its counts and reasons. **Partly
superseded:** the clause below sourcing that profile from Shaka Player's support
probe was reversed by [web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md) — the
measurement stands, its source is the browser's own APIs. The rest of this
record stands.
**Date:** 2026-07-22

## Context

[platform#25](0025-playback-consumer-and-media-origin.md) resolves a Part to bytes
and [web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md) plays them. Neither answers
the question that decides whether playback actually *works*: **the bytes a source
offers are frequently ones the client cannot decode.**

The constraint is sharper than it first looks — though **less sharp than this
record originally claimed, and the correction matters.** A browser has *two*
media paths, and they have different limits:

- **Media Source Extensions** (what Shaka and any adaptive stream use) accepts
  only fragmented MP4 and WebM. Matroska cannot pass through it whatever codec
  sits inside.
- **A plain `<video src>`** goes through the browser's own demuxer, which is far
  more permissive. Chromium's handles Matroska directly.

This record was written asserting the first as if it were the whole story, and a
live test proved otherwise: Chrome played a 4K HEVC Matroska over the direct-play
path without complaint. **So container is not the blocker for progressive
playback** — it becomes one only when MSE enters the picture.

Codec support is the real constraint, and it is narrower than container. HEVC
decodes where the platform supports it (Chrome on Windows 11, via the OS
extension). **AC3 and E-AC3 do not decode in Chrome at all**, in any container —
which is what actually stops a typical release playing.

The conventional answer is a transcoder. Jellyfin does it, and so does
[remux](https://github.com/lostb1t/remux), whose `playback/decision.rs` reads a
client `DeviceProfile`, calls `check_direct_play`, and falls back to building an
HLS transcode URL. It is the correct answer for a server whose library is *one
file per item* — when there is exactly one set of bytes, making them playable is
the only move available.

**Mosaic's situation is different, and the difference is the whole of this
decision.** A Stremio addon — an aggregator like AIOStreams especially — returns
*many* streams for one item: different releases, containers, codecs, audio
tracks, qualities, from many scrapers at once. The library is not one file per
item; it is a menu. When the menu contains an h264/AAC MP4 alongside twenty
HEVC/EAC3 MKVs, transcoding the MKV is doing expensive work to reach a result
that was already sitting in the list.

## Decision

**A client declares its capabilities; the Platform selects the stream that
client can play. Selection, not transcoding, is the primary answer to codec and
container mismatch. Transcoding is deferred, not designed around.**

- **The client declares a capability profile on `Attach`**
  ([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)) — containers, video codecs and
  audio codecs it can decode. The web client does not hand-maintain this: Shaka
  Player's static support probe reports what the browser actually supports, so
  the profile is measured rather than assumed, and a browser-version quirk does
  not become a server-side lookup table nobody maintains.
- **The Platform ranks candidates against the profile at play time.** Resolution
  gathers every stream the registry's `StreamProvider`s offer for the item,
  filters to those the profile can decode, then ranks the survivors by the
  quality signals [module-stremio-addons#1](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0001-completing-the-stremio-source-surface.md)
  already parses — resolution, size, swarm health. The best playable candidate
  becomes the ticket.
- **Selection happens per playback, never at import.** The profile differs
  between two clients of one install, so which release wins is not a property of
  the library. This is what [platform#24](0024-capability-gated-affordances.md)
  meant by "play-time resolution".
- **Candidates persist as Parts; only the resolved URL is transient.** The
  source's whole candidate set is written at import — the technical fields on
  `Part` exist precisely so something can choose between several Parts of one
  item — so selection reads the library rather than calling the source. The
  perishable half, the resolved URL, is cached separately and keyed by capability
  class ([platform#28](0028-resolution-cache-and-capability-classes.md)). An earlier
  draft called the Part's location an identity hint to be ignored; splitting
  durable candidate from perishable link is the better answer, and it is what
  keeps the aggregator off the play path.
- **Profiles reduce to a class.** Two clients declaring the same containers and
  codecs are one class, so caching and precomputation do not fragment per device
  or per person ([platform#28](0028-resolution-cache-and-capability-classes.md)).
- **Needing work is a ranking cost, not just a flag.** A candidate that must go
  through ffmpeg starts later than one that direct-plays, so an acceptable-quality
  MP4 should outrank a marginally better Matroska. Compatibility decides what is
  *possible*; this decides what is *pleasant*. **Refined by
  [platform#29](0029-probing-and-the-per-stream-playback-decision.md):** the cost is
  not one number. Copy-everything beats an audio-only encode, which beats a video
  encode; "remux or transcode" was too coarse an axis, and the per-stream plan is
  what ranking should actually price.
- **`StreamLink` grows the technical fields selection needs** — container, video
  codec, audio codec — completing what [module-stremio-addons#1](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0001-completing-the-stremio-source-surface.md) started when it added quality,
  size and seeders "so a future source-picker can rank and display candidates".
  Parsing is best-effort from release text and `behaviorHints`, exactly as the
  existing fields are, and a source that reports nothing leaves them zero.
- **A probe confirms the winner.** Release-name text lies, and an unparsed
  candidate is not the same as an incompatible one. **Superseded by
  [platform#29](0029-probing-and-the-per-stream-playback-decision.md):** a
  magic-number read identifies only the container, and it was the audio codec
  that usually decided playability. ffprobe against the resolved URL answers the
  whole question — container, video codec, every audio track — and its results
  persist on the Part. The parse still ranks the list; the probe settles the one
  about to play.
- **No playable candidate is an honest, specific failure**, not a silent one:
  the count of releases found and the reason each was rejected, rendered as a
  real state rather than a spinner that never resolves.
- **The user can override.** The source picker lists candidates with
  incompatible ones marked and explained. Automatic selection is a default, not a
  cage — a user who knows their setup can pick anything.
- **Bandwidth is a profile dimension, and selection prefers what fits.** The
  client reports measured throughput alongside its codec support, and ranking
  breaks ties toward candidates the connection can sustain. **Ordering candidates
  by bitrate needs no duration**: runtime is constant across releases of one
  item, so `SizeBytes` — already populated from `behaviorHints.videoSize` — is a
  valid proxy for comparing *these* candidates to each other. Absolute bitrate
  would need a duration, and `ContentMetadata.Runtime` is display-only by
  [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md), so selection deliberately does not
  depend on one.
- **Sustained stalling triggers reselection at the current position.** When
  playback repeatedly rebuffers, the Platform re-resolves to a lighter candidate
  and resumes where the user was. This is **not** seamless and is not presented
  as though it were: the viewer sees a rebuffer, and possibly a second or two of
  drift, because two releases do not share an exact timeline. It reuses machinery
  built anyway — "resume at position T" is
  [platform#26](0026-playback-state-is-platform-owned.md)'s mechanism and "pick a
  different candidate" is the ranker above — so the switch is a stall detector
  and some wiring rather than a subsystem.

**This makes the web client a deliberately limited-mode client, and that is
accepted.** Selection cannot manufacture a compatible release that no scraper
offers, and MP4 is scarce at high quality — much of what selection finds will be
h264/AAC at 720p or 1080p while the 2160p HEVC releases stay unplayable in a
browser. That trade is taken knowingly: a desktop client with libmpv is the real
answer to a fat profile, and it is deferred to a later thread.

## Alternatives considered

**Transcode on demand, as remux and Jellyfin do.** *Deferred, not rejected on
merit.* It is the right answer when there is one file per item, and Mosaic will
need it eventually — for a library of local files, and for the releases selection
cannot cover. But making it the *primary* mechanism here means paying an ffmpeg
subsystem, session lifecycle and hardware-acceleration story to reach a result
that is usually already in the candidate list. Selection first, transcoding when
selection demonstrably runs out.

**Stream-copy remux (MKV → fMP4) without re-encoding.** *Built, then superseded
in shape by [platform#29](0029-probing-and-the-per-stream-playback-decision.md).*
It shipped and it is genuinely cheap, but two of its premises were wrong. It
answered only the container, so an h264+AC3 release became a playable container
with undecodable audio — the per-stream decision (copy the video, encode only the
audio) is what actually plays it. And fragmented MP4 down a pipe cannot be
seeked, which HLS output fixes. The cheapness was real; the framing was too
coarse.

**Pick at import and store the choice.** *Rejected.* It bakes one client's
profile into shared library state, so a browser's limitation would degrade
playback for a desktop client on the same install, and it snapshots a link that
will have expired by play time.

**Let the client fetch candidates and choose.** *Rejected*, for the reason
[platform#24](0024-capability-gated-affordances.md) rejected client-side gating: it
puts selection policy, debrid credentials and release-name parsing into four
clients, each re-implementing it. The client knows what it can decode; the server
knows what is on offer. Declaring the first and deciding on the second is the
correct division.

**Seamless adaptive bitrate across candidates, switching renditions mid-stream as
YouTube and Netflix do.** *Rejected — it cannot be built from these inputs, and
that is structural rather than a matter of effort.* ABR requires renditions that
are one encode at several bitrates: segment boundaries on aligned keyframes, a
shared timeline and an identical timescale, so a player can append a segment from
a different rendition into the same buffer without a visible seam. Stremio
candidates are *different releases by different groups* — different GOP
structures, different keyframe positions, frequently different runtimes (varying
intros, edits, framerate conversions) — delivered as progressive files with no
segments to switch at and no common timeline to switch on. **True ABR therefore
requires transcoding one source into a ladder**, which is the deferred slice
below; there is no path to it that avoids an encoder. Recorded explicitly because
"why can't Mosaic adapt quality like Netflix" is a question that will recur, and
the answer is not obvious from the outside.

**A full Jellyfin-style `DeviceProfile`** — codec conditions, bitrate ceilings,
resolution limits, subtitle delivery methods. *Rejected as premature.* Most of
that surface exists to parameterise a transcoder. With selection as the
mechanism, containers and codecs carry nearly all the decision, and the profile
can grow when something needs it.

## Consequences

- **The profile earns its keep immediately, with no transcoder behind it.** This
  is the payoff for capability negotiation: it does real work in the first slice
  rather than waiting for transcoding to exist.
- **Provider precedence gets a real answer** — [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)'s
  open seam. Candidates rank by compatibility first, then quality, with the user
  able to override. The seam is closed for foreground playback; background
  auto-play inherits the same ranking.
- **Aggregator addons become a strength rather than noise.** AIOStreams returning
  forty candidates is exactly what makes selection viable; a single-scraper source
  would leave nothing to choose between.
- **Selection quality depends on parsing quality.** Release-name text is
  adversarial and inconsistent; the probe bounds how wrong the parse can be, but
  it does not make the parse good. Expect this to need iteration against real
  AIOStreams output.
- **Web is honestly limited, and says so.** Some items will have no playable
  candidate. Naming that in the UI, with counts and reasons, is the difference
  between a known constraint and a bug report.
- **Quality adaptation is coarse, and honestly so.** Selection plus
  stall-triggered reselection gives a real response to a degrading connection —
  it just costs a visible rebuffer where a commercial service would glide. That
  is the ceiling of what a menu of unrelated releases can deliver, and the UI
  should not imply otherwise.
- **The failure data is the input to the next decision.** How often selection
  finds nothing, how often it reselects downward, and what it rejected, is what
  says whether the next slice is stream-copy remux, full transcoding, or the
  desktop client.

## Implementation implications

SDK: container, video-codec and audio-codec fields on `StreamLink`, filled by the
module's dialect translation ([module-stremio-addons#2](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0002-modules-as-anti-corruption-layers.md));
the client capability profile on the session `Attach`
([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)). Module: extend
`parseStreamMeta` to decode container and codecs from release text and
`behaviorHints.filename`. Platform: candidate gathering, profile filtering and
ranking inside `ResolvePlayback`
([platform#25](0025-playback-consumer-and-media-origin.md)), the container probe,
and the no-candidate state on the emit-side. Web: Shaka Player, with its support
probe feeding the declared profile, and the source picker rendering candidates
with incompatible ones marked.
