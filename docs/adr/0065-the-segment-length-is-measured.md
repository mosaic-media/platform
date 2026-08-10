# The segment length is measured from the source, not chosen

**Status:** **Superseded wholly by
[platform#66](0066-the-playlist-is-a-nominal-grid.md), and never built.** Both
decisions here are wrong. The head-only probe does not exist —
`-read_intervals` bounds what ffprobe reports and not what it reads, and a
20-second window transfers 100% of a faststart MP4; the "93 ms" cited below was
wall-clock on a small local file. And matching the source's interval is
unnecessary, because the origin restarts ffmpeg at the position a client asks
for, so the playlist is a nominal grid rather than a description of a continuous
run. This record superseded decision points 1 and 3 of
[platform#64](0064-the-transcoded-stream-is-segmented.md); with this one set aside,
[platform#64](0064-the-transcoded-stream-is-segmented.md)'s decision to segment and its points 2, 4 and 5 stand, and [platform#66](0066-the-playlist-is-a-nominal-grid.md)
replaces the rest.
**Date:** 2026-07-30

## Context

[platform#64](0064-the-transcoded-stream-is-segmented.md) decided that a
transcoded stream is served as HLS with a playlist computed from the probed
duration, and set a **constant** six-second segment. It then conceded, in
decision point 3, that where the video is copied "ffmpeg cuts at the next
keyframe in the source and real durations drift from advertised ones", and
called that drift "tolerable".

Two things were wrong with that, and the second one is not a matter of degree.

**First, it is the combination `remux` refuses.** `remux` serves a computed
uniform playlist only for MPEG-TS. The moment it uses fragmented MP4 —
`use_fmp4()`, which is video-copy of an HEVC source — it abandons the computed
playlist and serves ffmpeg's own rolling `EVENT` playlist instead, upgrading it
to `VOD` only once ffmpeg has written `EXT-X-ENDLIST`. Its comment gives the
reason: fMP4 segments snap to keyframe boundaries, so the playlist must reflect
real timing. [platform#64](0064-the-transcoded-stream-is-segmented.md) specified a computed playlist *and* fMP4, which is the one
pairing that record avoids.

**A rolling playlist is not available to Mosaic**, whatever remux does with it.
A viewer opening a two-and-a-half-hour film must see two and a half hours of
scrubber immediately, not a timeline that grows as the encoder advances. Resume
is the release requirement this milestone exists for, and resuming to ninety
minutes cannot wait for the encoder to reach ninety minutes.

**Second, "drift" understates it.** Measured against real ffmpeg rather than
reasoned about:

| Video | `-hls_time` | Source keyframes | What ffmpeg produced |
|---|---|---|---|
| copy | 6 s | every 10 s | **six segments of 10 s** |
| encode, `-force_key_frames` | 6 s | forced | **ten segments of 6.000000 s** |
| copy | 10 s | every 10 s | **six segments of 10.000000 s** |
| copy | 10 s | every 9.96 s | **19.92 s**, then 9.96 × 4, then 0.24 s |

A copied stream is not approximately six seconds per segment. It is exactly one
**source keyframe interval** per segment, whatever was asked for, because a
segment must begin at a keyframe and a copy cannot make one. A playlist claiming
ten six-second segments therefore describes six ten-second ones: not a
timeline that is slightly off, a timeline whose segments do not exist at the
positions it names.

The fourth row is the trap. Asking for *more* than the real interval — ten
against 9.96 — makes ffmpeg skip a keyframe and emit a doubled first segment,
because at 9.96 the elapsed time has not yet reached the target. **The request
must be at or below the true interval, never above it.**

The third row is the answer. When the two agree, a copied stream segments
exactly, and the computed playlist is true.

## Decision

**The segment length is measured from the source rather than chosen, and the
playlist advertises what the source will actually produce.**

1. **Where the video is re-encoded, the origin chooses.** `-force_key_frames`
   places a keyframe on every boundary and the playlist is exact by
   construction, at whatever interval the origin prefers.

2. **Where the video is copied, the source chooses and the origin measures.** A
   head-only probe reads the keyframe timestamps of the first seconds of the
   file — `ffprobe -read_intervals`, which returned in **93 ms** and costs a
   couple of ranged fetches against a remote source. The interval it reports is
   the segment length; the playlist advertises that, and ffmpeg is asked for a
   value comfortably below it so it cuts at every keyframe rather than skipping
   one.

   This is **not** [platform#64](0064-the-transcoded-stream-is-segmented.md)'s
   rejected keyframe *index*. Seanime reads every video packet in the file to
   learn where every keyframe is, which over a remote source means transferring
   it. This reads the head and learns one number.

3. **An irregular source is detected, not guessed at.** The same probe says
   whether the spacing is regular. Where it is not, no uniform playlist can be
   true, and the honest answers are to re-encode the video — which makes the
   boundaries ours — or to decline to segment. Emitting a playlist that names
   positions the file does not have is not among them.

4. **The playlist stays complete and stays `VOD`.** This is the half of
   [platform#64](0064-the-transcoded-stream-is-segmented.md) that was right and it is the requirement everything else serves: every
   segment listed before a byte is produced, `EXT-X-ENDLIST` written, so the
   whole running time is scrubbable from the first frame.

## Alternatives

**Tolerate the mismatch, as `remux` does for MPEG-TS.** *Rejected.* The
measurement shows it is not a tolerance question: the segments are not near the
positions the playlist names, they are at entirely different ones.

**Serve ffmpeg's own playlist, as `remux` does for fMP4.** *Rejected.* Accurate,
and it makes a film a live stream until the transcode finishes, which fails the
requirement this milestone exists to satisfy.

**Re-encode the video whenever we segment, so boundaries are always ours.**
*Rejected as the default, kept as the fallback for point 3.* It is correct and
it is the most expensive thing the system can do — a 4K HEVC source measured
around 30× slower than realtime — so making it the price of seeking would make
seeking cost more than the release it is seeking in.

**Pick a segment length short enough to divide any keyframe interval.**
*Rejected*, because no such length exists: the cut still lands on the next
keyframe whatever is asked for, so a one-second request against a ten-second
interval still produces ten-second segments. Asking for less does not make them
smaller; it only stops them being doubled.

## Consequences

- **Segment length becomes a per-playback value rather than a constant**, which
  is a change to the shape of the playlist code, not only to its inputs.
- **The head probe joins the existing probe** ([platform#29](0029-probing-and-the-per-stream-playback-decision.md)) rather than replacing
  it: one settles what the release *is*, this settles where it can be cut. Both
  are cheap, both are cached on the Part, and the second only runs when the
  first says the video will be copied.
- **A release with an irregular GOP is a named degradation** rather than a
  silently wrong playlist. This is the case that will produce support questions,
  and it is better to say "this release cannot be seeked while transcoding" than
  to send a viewer to a position that does not exist.
- **`remux`'s wall is removed rather than routed around.** Its rolling playlist
  for fMP4 is a consequence of never measuring the source; with the interval
  known, a computed playlist and fragmented MP4 are compatible, and the whole
  running time stays scrubbable.
