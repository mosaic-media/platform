# The transcoded stream is segmented, not byte-addressed

**Status:** Accepted. **Built on both sides, and never played.**
`internal/transport/playback` serves `index.m3u8`, `init.mp4` and numbered
segments; `@mosaic-media/sdui-react` `0.22.0` reads them. Nothing has been
watched through it — the row stays on the
[register](../unreachable-capability.md#the-segmented-playback-origin).
Supersedes decision point 2 of
[platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md); that record's
measurement and its decision points 1 and 3 stand unchanged.
**Decision points 1 and 3 are in turn superseded by
[platform#65](0065-the-segment-length-is-measured.md)** — the constant segment
length was wrong and the drift it called tolerable was measured and is not; the
decision to segment, and points 2, 4 and 5, stand.
**Date:** 2026-07-30

## Context

[platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md) measured that a
debrid CDN honours `Range`, and concluded from it that a transcoded stream could
be made seekable by *byte*: advertise a length, map a requested offset onto a
timestamp, restart ffmpeg there. Its own status line already records the first
half of what went wrong — a media element issues overlapping, opportunistic
ranges, so answering each from its own ffmpeg produced a file assembled from two
different timestamps and the decoder refused it.

The spool architecture fixed *that*. One transcode per region of a playback
writes to one file and every reader sees the same bytes; three per ticket,
because a media element reads a head and a tail at once and a bound of one meant
the client's second region destroyed its first. Live, `MEDIA_ERR_DECODE` is
gone, `readyState` reaches 4, and seeks land on the clock.

**What it cannot fix is the length.** `serveSeekableRemux` advertises the
source's size and then writes however many bytes ffmpeg actually produces. For a
remux that copies video and re-encodes only audio the two are close. For a
release that reaches ffmpeg because its video must be re-encoded they are not: a
61 GB 4K source becoming a 1080p h264 output differs by an order of magnitude, so
the response is truncated against its own `Content-Length` and the end of the
timeline is unreachable. The estimate can be made *good* — advertising the probed
source size instead of a flat bitrate closed a factor of twenty-two — and it
cannot be made *exact*, because the true length is unknowable until the transcode
finishes and the transcode is the thing the length is supposed to describe.

A second, independent problem sits beside it. Nothing bounds how far ahead of the
viewer the transcode runs. An audio-only remux proceeds at near-copy speed, so
one click on Play writes the entire release into a spool as fast as the upstream
will serve it. Reaping an abandoned session bounds the *leak*; it does not bound
the *rate*.

### What the reference implementations do

Both answer this with HLS, and the difference between them decides which
variant applies here.

- **`remux`** generates a VOD playlist server-side listing every segment from
  zero to the end at a uniform length, so a client may seek anywhere
  immediately; each segment URL carries its own cumulative start time, and a
  request for one that does not exist restarts ffmpeg at that offset. It bounds
  itself two ways Mosaic does not: it `SIGSTOP`s the encoder when it runs 300 s
  ahead of the playhead, and deletes segments 30 s behind it.
- **`seanime`** builds an *exact* playlist from the source's real keyframe
  index, extracted with `ffprobe -show_entries packet=pts_time,flags`.

**Seanime's variant is not available to Mosaic**, and the reason is the same
measurement [platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md) made. Reading every video packet's timestamp over a remote
61 GB URL means transferring the file — the opposite of the one ranged fetch that
made restart-at-offset affordable. Seanime's own fallback, synthetic keyframes at
a flat two seconds when extraction yields nothing, concedes the case; and
seanime's transcoder is local-file only, so it never had to face it.

## Decision

**The origin serves a transcoded stream as HLS with a uniform VOD playlist, and
the web client adopts a media framework to play it.**

1. **The playlist is computed, not observed.** Segment length is a constant and
   the count is the probed duration divided by it, so the whole playlist is
   emitted before a byte has been produced, with `#EXT-X-PLAYLIST-TYPE:VOD` and a
   closing `#EXT-X-ENDLIST`. A client can therefore seek anywhere at once. This
   is precisely the property the advertised `Content-Length` was synthesising,
   obtained honestly: a duration the probe measured, rather than a size nobody
   can know.

2. **A segment request is a position, and the restart machinery is reused.**
   Serving segment *N* means having a transcode whose output covers it;
   otherwise one starts at *N* × segment length, with `-ss` before `-i` and
   `-copyts`. Those are [platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md)'s flags and its arithmetic, re-keyed from a byte
   offset to a segment index — and they stay affordable for exactly the reason
   that record measured.

3. **Segment boundaries are nominal where the video is copied, and that is
   accepted rather than solved.** Where video is re-encoded, `-force_key_frames`
   puts a keyframe on every boundary and the playlist is exact. Where video is
   copied there is no such control: ffmpeg cuts at the next keyframe in the
   source and real durations drift from advertised ones. The drift is tolerable
   because `#EXTINF` is advisory and `-copyts` makes the timestamps inside the
   segments authoritative. Jellyfin and `remux` both live with this.

4. **Production is bounded ahead of the playhead and segments are evicted behind
   it.** This is not an optimisation to add later; it is the half of the design
   that makes the storage finite, and it is what a single spool per playback
   structurally cannot have, because a monolithic file offers no unit to evict.

5. **The web client adopts a media framework. This is
   [web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md)'s condition firing, not
   [web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md) being reversed.** That record chose a bare `<video>` element and said
   in terms that the client "adopts a media framework when the Platform serves
   something a `<video>` element cannot play — which today means HLS". It is now
   about to. Safari plays HLS natively; every other browser needs the library.

**Direct play is untouched.** A release needing no work is still relayed
byte-for-byte with `Range` passed through, which was always seekable and remains
the cheapest path. HLS applies only to the streams ffmpeg produces.

**Segments are fragmented MP4 rather than MPEG-TS.** The origin already emits
fMP4, the client profile accepts HEVC, and Apple's HLS authoring rules require
fMP4 for HEVC — MPEG-TS cannot carry it correctly.

## Alternatives

**Keep byte addressing and bound the spool.** *Rejected.* It is the smaller
change and it leaves the defect: the advertised length is structurally an
estimate, so a re-encoded release still truncates against its own
`Content-Length`. "Resume that works only on some releases is not resume" is the
requirement M3 was written against, and this alternative satisfies it for the
copy case only.

**Extract a real keyframe index, as `seanime` does.** *Rejected*, for the reason
above: over a remote source it costs the whole file, which is the cost [platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md)'s
measurement exists to avoid.

**Serve a full adaptive-bitrate ladder.** *Out of scope, not rejected.* Real ABR
needs aligned renditions, and a menu of unrelated releases cannot supply them at
any level of effort — the roadmap already records this as the reason full
transcoding is deferred. One rendition is what this decides; the playlist shape
does not preclude more later.

**Keep the whole transcode, as a cache.** *Rejected.* This is remote playback: a
release is fetched from a third party under a short-lived credential and is not
Mosaic's to retain. Keeping it would also be the largest thing on disk in the
whole system, for a re-watch that will re-resolve anyway.

## Consequences

- **The byte-addressing code is deleted, not left beside its replacement**:
  `contentLength`, `offsetAt`, `parseByteRangeStart` and `serveSeekableRemux`,
  with their tests. `ShouldRemux` — dead code [platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md) named and deliberately did
  not delete — goes in the same change, because the container heuristic it
  encodes has no caller in either design.
- **`Spool` survives and becomes what it was shaped for.** It is already a port
  with a substitutable factory; segmentation supplies the bounded working set
  that lets the implementation be memory rather than a file. That is the
  intended end state and the size is an honest constraint rather than a free
  win: a copied 4K video stream is around 8 MB/s, so a window measured in
  minutes is measured in hundreds of megabytes.
- **A client that cannot run the framework loses transcoded playback**, where
  today it gets an unseekable stream. That is a real loss and it is the price of
  the decision; the relayed path still works for every release needing no
  transform, and no client other than the Shell exists to be affected yet.
- **`MimeType` on the `Player` node becomes load-bearing.** It exists and is
  already set for a remuxed stream; it is what tells a client which pipeline to
  choose before it fetches anything.
- **Resume becomes exact on a transcoded stream**, which is the release
  requirement this milestone exists to satisfy, and the reason "seek and resume
  on a remuxed stream" was listed as owed rather than deferred.
- **A slower-than-realtime encode is still slower than realtime.** [platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md)'s
  consequence stands verbatim: segmenting makes a stream seekable, it does not
  make it arrive. The honest test of this path is a release needing only an audio
  encode.
