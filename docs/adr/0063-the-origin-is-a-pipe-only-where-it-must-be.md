# The origin is a pipe only where it must be

**Status:** Accepted, and **partly wrong as written**. The measurement stands.
Decision point 2's inference does not: a ranging upstream makes an offset
restart *cheap*, and does not make per-range restart *correct*. Serving each
client range from its own ffmpeg was built, tested and disproved live — a media
element issues overlapping ranges, so one playback drew bytes from two
transcodes at different timestamps and would not decode. The file-backed design
this record dismisses as unnecessary is required by the client's range
behaviour, not by the upstream's.
**Decision point 2 is superseded by
[platform#64](0064-the-transcoded-stream-is-segmented.md)**, which takes the
alternative this record left open; decision points 1 and 3 stand.
**Date:** 2026-07-27

## Context

[platform#25](0025-playback-consumer-and-media-origin.md) says ranges and seeking
"come free" because the origin relays an upstream that supports them, and records
in its own status line that the property holds for a relayed stream and **not**
for the remuxed one [platform#27](0027-stream-selection-against-a-client-profile.md)
and [platform#29](0029-probing-and-the-per-stream-playback-decision.md) introduced.

That is correct and it left one thing unstated, which turned out to be carrying
the weight: **a relayed stream is seekable only if the upstream honours Range**,
and nothing had ever checked that against a real debrid CDN. The roadmap's
phrasing — "the origin emits fragmented MP4 off a pipe, `Accept-Ranges: none`" —
was in consequence being read as a property of *the source* rather than of this
origin's implementation, and the whole of M3 slice 4 was scoped against that
reading.

A second premise sat underneath it: that Matroska is the irreducible case,
because Media Source Extensions accept only fragmented MP4 and WebM. That is
true of MSE.

## The measurement

`platform/tools/rangeprobe` against two AIOStreams resolutions of one 4K release
through a debrid profile, on the URLs the origin actually fetches:

| Upstream | `HEAD` | Ranged `GET` | Mid-file bytes |
|---|---|---|---|
| A | `200`, `Accept-Ranges: bytes`, 61.4 GB | `206`, correct `Content-Range` | differ from the head |
| B | **`405 Method Not Allowed`** | `206`, correct `Content-Range` | differ from the head |

Both honour Range. The mid-file comparison is the part that matters: a CDN that
does not implement Range commonly answers `200` with the whole body, or `206`
counted from byte zero, and a status-code check reads both as success.

Upstream B refusing `HEAD` while ranging perfectly well is worth recording on its
own — a probe that learns size from `HEAD` alone concludes the file is 31 bytes.

## Decision

**The origin stays a byte relay wherever it can, and is a pipe only for streams
ffmpeg must produce.** Three things follow, and the third is the one that
changed.

1. **The relayed path is seekable and needs nothing built.** `Handler` already
   forwards `Range` and `If-Range` and relays `Content-Range`, `Accept-Ranges`
   and the `206`. The measurement supplies the missing half of the claim.

2. **The segmenter may seek its source over HTTP.** A keyframe-aligned segment
   can be produced with `ffmpeg -ss` for the cost of one ranged fetch, so
   restart-at-offset is cheap. The alternative design — a file-backed cache
   accumulating the source once, which is what `seanime`'s non-local path does
   because it assumes upstream Range with no fallback — is **not** needed. This
   is the decision the measurement was for: the two are not variations, they are
   different systems, and building the wrong one is expensive.

3. **Matroska is not the boundary, because the player is not MSE.** The Shell
   renders a bare `<video src>` ([web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md)),
   whose native demuxer handles Matroska; the client profile declares
   `containers: []` deliberately and says why. Nothing in the per-stream decision
   consults the container at all — `Plan.DirectPlay` is
   `Video == Copy && Audio == Copy` — and `ShouldRemux`, which encodes the
   container rule, **is called from nowhere in production.**

   So the set that cannot be seeked is not a container subset. It is the set of
   releases that reach ffmpeg *for any reason*: an undecodable video codec, HDR
   needing a tone-map, or audio the client cannot decode. That set is larger than
   "Matroska" in some directions and much smaller in others — a 1080p h264+AAC
   MKV direct-plays today and always did.

**The output container is a second constraint on the audio decision.** The client
profile answers "can this be decoded"; the muxer answers "can this be carried".
Only the first was being asked, and the two disagree: Chrome decodes FLAC and
fragmented MP4 refuses it. The constraint binds only once ffmpeg is already
involved — while a stream is relayed the container is the source's own.

**A failed transform is not a successful empty stream.** The origin must know it
has bytes before it writes a status.

## Alternatives

**Assume the upstream ranges and build accordingly.** *Rejected*, and this record
exists partly to say why it was worth the delay. The two candidate designs differ
in their storage model, their failure modes and their cost; the assumption was
load-bearing and cheap to check.

**Serve HLS to a hls.js client.** *Not chosen here.* It is the conventional answer
and it needs a client library, which [web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md) deliberately does not have. Left
open rather than refused: if segmenting a bare `<video>` proves unworkable, this
is the fallback, and it is a client decision rather than an origin one.

**Cap selection at the encoder's ceiling.** *Rejected.* A declared display height
is a true statement about the panel and the right input to *selection*; using it
as the encode ceiling asked this Platform for a real-time 4K tone-mapped encode.
Capping selection instead would hand a 4K display a 1080p release to upscale. The
two caps are separate numbers answering separate questions.

## Consequences

- **The seekable set is now stated in terms of work, not containers**, which is
  what the code always did and the documents did not.
- **`ShouldRemux` is dead code.** It is left in place with this record naming it
  rather than deleted in the same change that discovered it; deleting it is a
  decision about the container heuristic's future, not about this measurement.
- **A slower-than-realtime encode is not a seeking problem and segmenting will
  not fix it.** The release measured here runs about 30× slower than realtime
  even bounded to 1080p, because decoding 4K 10-bit HEVC in software is the
  floor. Segmenting makes a stream seekable; it does not make it arrive.
- **The honest test of the segmented path is a release needing only an audio
  encode**, which remuxes at near-copy speed.
- The measurement is reproducible: `go run ./tools/rangeprobe` with an instance
  URL in the environment. It reaches a live third-party CDN with a developer's
  credential, so it is a tool and never a CI test.
