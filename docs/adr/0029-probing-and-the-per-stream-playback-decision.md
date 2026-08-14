# Probing, and the per-stream playback decision

**Status:** Accepted; **built.** The probe, the per-stream plan and the
audio-encode-video-copy path are built, and implementation added one thing this
record does not anticipate: HDR is tone-mapped rather than copied, because
passing HDR10 metadata to an SDR browser decoder produces a purple-and-green
picture. **The HLS emission below has landed since**, by a route this record
does not describe: [platform#82](0082-the-origin-relays-or-serves-a-nominal-segment-grid.md) and
[platform#82](0082-the-origin-relays-or-serves-a-nominal-segment-grid.md) make the transcoded stream a
computed playlist over restart-addressed segments, so anything encoded is served
as `index.m3u8`, `init.mp4` and numbered segments and is seekable. The
unseekable fragmented-MP4 pipe survives only where a source reports no duration
— there is no grid to compute — and answers `Accept-Ranges: none` there. **Nothing
has been watched through the segmented path**, which is [platform#82](0082-the-origin-relays-or-serves-a-nominal-segment-grid.md)'s own status
line and a row on the [register](../unreachable-capability.md).
**Probe results are now durable on the Part, with one departure:** the technical
columns this record points at cannot hold a track list, and the four-audio-track
release below is exactly why that matters — so the full result is stored as a
versioned document in `Part.Attributes` and the columns carry the summary. The
probe is authoritative over the module's parse, as decided here. Recording it
authorises `content.bind`, and the system principal it was waiting on is built:
the playback transport records the probe as that principal, so a read-only
viewer warms the cache instead of re-probing every play.
**Date:** 2026-07-22

## Context

Slice 1 shipped and then failed on its first real stream, which is what it was
for. The failure is worth recording precisely, because two of the assumptions
underneath [platform#27](0027-stream-selection-against-a-client-profile.md) were
wrong and the evidence is concrete.

An AIOStreams instance returned 59 candidates for one film: 47 Matroska, 6 MP4,
one AVI. The module took the first, and the resolved URL looked like this:

```
https://comet.feels.legal/<opaque>/…MIRCrew.mkv&name=Thor%3A%20Ragnarok&media_id=tt3501632
```

**The container hint was in a query parameter, not the path.** [platform#27](0027-stream-selection-against-a-client-profile.md)'s
extension heuristic strips the query string and reads the path extension, so it
saw none, decided no rewrite was needed, and would have relayed ten gigabytes of
Matroska to a browser that cannot open it. A range probe confirmed it: `206`,
`Content-Type: application/octet-stream`, first four bytes `1a45dfa3`.

Two lessons, and the second one matters more.

**Parsing is the wrong source of truth.** [platform#27](0027-stream-selection-against-a-client-profile.md) planned to parse container and
codecs from release text and `behaviorHints.filename`, with a magic-number probe
as a backstop. Release names are adversarial, addons put the same fact in
different places, and a magic number identifies only the container — it says
nothing about whether the audio inside is AC3, which is the thing that actually
decides playability.

**"Remux or transcode" is the wrong axis.** [platform#27](0027-stream-selection-against-a-client-profile.md) and the code written for it
treat a container rewrite as cheap and a transcode as expensive, and the file as
one or the other. [remux](https://github.com/lostb1t/remux) plays the same
release in the same browser, and its `playback/engine.rs` shows why: the decision
is made **per stream**. Video is copied unless something forces otherwise; audio
is re-encoded only when the target cannot carry it. An h264 + AC3 Matroska
becomes *copy the video, transcode the audio* — near-free on CPU, and playable.
The earlier framing predicted "video with no audio" for exactly the case that
works.

Its `playback/probe.rs` shows the source of truth it uses instead of parsing:
ffprobe, run against the remote URL, cached in the database.

**A live test then corrected the premise underneath both records.** The chain was
run against a real release and ffprobe reported it exactly:

```
container: matroska  ·  31.98 GB
video: hevc 3840x2160 Main 10
audio: eac3 6ch (hin) · eac3 (tam) · eac3 (tel) · eac3 (eng)
subs:  subrip x4
```

Chrome **played it** — video, no audio. Three things follow, and each narrows the
problem:

- **The container was never the blocker on this path.** MSE takes only fMP4 and
  WebM, but a plain `<video src>` uses the browser's own demuxer, which handles
  Matroska. The MSE limit binds Shaka and adaptive streaming, not direct play,
  and [platform#27](0027-stream-selection-against-a-client-profile.md) overstated it.
- **The container rewrite therefore solves less than it appeared to.** It earns
  its place for MSE later; it is not what stands between a user and audio.
- **One codec is the whole failure.** E-AC3 does not decode in Chrome in any
  container. Copy the HEVC video untouched, re-encode only that audio — on a
  32 GB 4K file, the difference between near-free and unusable.

And a requirement that only a real file surfaces: **there are four audio tracks
and the first is Hindi.** Mapping `0:a:0` gives Hindi audio on an English film,
so track *selection by language* is not a refinement to add later — it is part
of the plan or the plan is wrong.

## Decision

**Probe the bytes rather than parse their name, and decide per stream rather than
per file. Copy what the client can take, encode only what it cannot, and emit
HLS when anything is encoded so the result stays seekable.**

- **ffprobe is the authority on what a stream is.** Run against the resolved URL
  — ffprobe range-requests the header rather than downloading the file — it
  yields container, video codec, resolution, HDR type, and every audio and
  subtitle track with its codec, channels and language. That is the whole input
  the decision needs, and none of it is guessed.
- **Probe results are durable and live on the Part.** They describe the bytes,
  not the viewer or the moment, so they belong with the candidate in
  [platform#28](0028-resolution-cache-and-capability-classes.md)'s durable tier —
  and `Part` already has `Container`, `VideoCodec`, `AudioCodec`, `Width`,
  `Height`, `HDRFormat` and `BitrateBPS` sitting empty waiting for them. Probe
  once, reuse forever; a re-resolved URL for the same release does not re-probe.
- **Probing is not free, so it is not universal.** It costs a request and a
  process per candidate, and a listing can be sixty long. Probe the candidate
  about to be played, not the whole list: the ACL's parsed metadata
  ([module-stremio-addons#2](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0002-modules-as-anti-corruption-layers.md)) is good enough to
  *rank*, and the probe confirms the *winner* before a ticket is minted.
- **The decision is per stream, and includes *which* stream.** Each of video,
  audio and subtitles is independently copy-or-encode against the client's
  declared profile ([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)). The common
  real case — video the browser accepts, AC3/EAC3/DTS audio it does not — is a
  video copy and an audio encode; treating it as a whole-file transcode would
  burn CPU re-encoding a stream that needed nothing.
- **Track selection is part of the plan.** A release routinely carries several
  audio tracks in several languages, and the first is not the likely one. The
  plan names the track by language preference before it decides how to carry it,
  because a perfect encode of the wrong language is still the wrong film.
- **Anything encoded is emitted as HLS.** This supersedes the fragmented-MP4-down-
  a-pipe approach and its worst property. A pipe has no index, so [platform#25](0025-playback-consumer-and-media-origin.md)
  recorded that a remuxed stream cannot be seeked; segments fix that, because a
  seek is a segment request. Seeking into a not-yet-produced region restarts
  ffmpeg with `-ss` **before** `-i` (a fast seek rather than a decode-and-discard)
  and aligns segment numbering with
  `start_number = floor(start_time / segment_length)`, which is how the resumed
  stream and the playlist agree on where they are.
- **A pure copy of everything still needs no encoder** and can stay progressive.
  When the client takes every stream as-is, the origin relays
  ([platform#25](0025-playback-consumer-and-media-origin.md)) and keeps byte-range
  seeking for free. HLS is the path for *encoded* output, not a replacement for
  direct play.
- **Ranking prefers less work.** A candidate that direct-plays outranks one
  needing an audio encode, which outranks one needing a video encode, quality
  being close. This is [platform#27](0027-stream-selection-against-a-client-profile.md)'s "remux is a ranking cost" restated at the
  right granularity.
- **The producer is bounded.** ffmpeg run against a fifty-gigabyte source will
  happily race ahead of a viewer; it is paused and resumed to hold a bounded
  lead rather than transcoding a whole film nobody is watching yet.

## Alternatives considered

**Parse container and codecs from release names and `behaviorHints`.**
*Rejected as the authority, kept as a ranking hint.* It is what [platform#27](0027-stream-selection-against-a-client-profile.md)
specified and it is what failed: the fact lived in a query parameter. It also
cannot answer the audio question at all, and audio is what usually breaks
playback. It stays useful for ordering sixty candidates cheaply, where being
occasionally wrong costs a re-rank rather than a failed play.

**A magic-number container probe** ([platform#27](0027-stream-selection-against-a-client-profile.md)'s backstop). *Superseded.* One
ranged read identifies the container and nothing else, so it would have caught
this Matroska and still handed the browser undecodable audio. ffprobe costs
marginally more and answers the whole question.

**Probe every candidate at import.** *Rejected.* Sixty ffprobe invocations per
item, against links that expire, for a list of which one gets played. Probe the
winner.

**Whole-file transcode when anything is incompatible.** *Rejected.* It re-encodes
video that needed nothing, which is the expensive part, to fix audio that is
cheap. Per-stream is barely more code and enormously less work.

**Keep fragmented MP4 down a pipe.** *Rejected.* It is simpler and it cannot
seek, which for a two-hour film is not a limitation anyone will accept. HLS is
more machinery for a property that is not optional.

## Consequences

- **ffmpeg and ffprobe become real dependencies** for anything but a pure direct
  play. The Platform must still boot and direct-play without them, and say so —
  but "install ffmpeg" moves from a nicety to the thing that makes most content
  work.
- **The 501-when-absent path gets more common and more specific.** Without
  ffmpeg the answer is no longer "Matroska will not play" but "this release needs
  its audio re-encoded"; the message should say which.
- **[platform#25](0025-playback-consumer-and-media-origin.md)'s non-seekable caveat is retired** for encoded output. It stands
  only for as long as fragmented-MP4-down-a-pipe exists, which this replaces.
- **Sessions become stateful.** An HLS transcode has a working directory, a
  lifetime, a segment list and a producer process to reap — the first thing in
  the playback path that is not a request/response. It is also the first real
  pressure for the module lifecycle and scratch-storage grant [platform#25](0025-playback-consumer-and-media-origin.md) deferred,
  even though this sits on the Platform rather than in a module.
- **Hardware acceleration becomes a question worth having.** Audio-only encodes
  are cheap enough on any CPU; the moment a video encode is unavoidable
  (HEVC to a client without it) a software encoder on a home server is not
  going to keep up.
- **Probe data enriches the library for free.** Resolution, HDR format and audio
  languages are exactly what a detail screen and a source picker want to show,
  and they arrive as a side effect of deciding how to play.

## Implementation implications

A probe step in the Platform's playback path, writing its results onto the Part
through the content surface and skipping when they are already there. A decision
function taking probe data plus the client profile and returning a per-stream
plan. An HLS session on the origin — working directory, playlist, segment
serving, `-ss`/`start_number` seek handling, producer throttling and reaping —
alongside the existing relay path, which stays the answer whenever the plan is
copy-everything. `Part`'s technical fields get populated for the first time.
