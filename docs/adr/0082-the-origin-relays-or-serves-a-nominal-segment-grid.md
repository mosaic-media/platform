# The origin relays where it can, and serves a nominal segment grid where it cannot

**Status:** Accepted. **Built on both sides, and never played.**
`internal/transport/playback` serves `index.m3u8`, `init.mp4` and numbered
segments, the nominal grid is computed in `playlist.go` and the restart-at-`-ss`
in `segments.go`, and `@mosaic-media/sdui-react` `0.22.0` reads them. No release
has been watched through it — the row stays on the
[register](../unreachable-capability.md#the-segmented-playback-origin).
Consolidates the four records of the segmented-origin run, whose bodies this
replaces. Their numbers are retired and stay retired, so they are named
throughout as the first through fourth attempt rather than cited — a citation
would either dangle or, worse, resolve to a later record that happens to hold
the number. The first three are the **Design 1**, **2** and **3** sections
below; the fourth is the decision this record carries. **Three of the four were partly or wholly wrong as written**, and
the corrections are kept below rather than in this line: the **first** was
*accepted, and partly wrong as written* — its measurement stood and its
byte-addressing inference was disproved live; the **second**'s decision points 1
and 3 were superseded, its constant segment length being wrong and the drift it
called tolerable being measured and not; and the **third** was **superseded
wholly and never built**, both of its decisions wrong. The four measurements all
stand and are reproduced here. This record replaces the numbers, not the history.
**Date:** 2026-08-10

## Context

Making a stream seekable was attempted four times in four days. The first three
attempts failed, each for a different reason, and each failure was found by
measuring something the previous record had reasoned about. The measurements are
the durable part of the run and they are all here, so that the next reader does
not repeat them.

The four records superseded each other in parts rather than wholesale, and the
chain is worth stating exactly, because most of each record survived the record
that corrected it. The **second** superseded **decision point 2 of the first**,
leaving that record's measurement and its decision points 1 and 3 standing
unchanged. The **third** then superseded **decision points 1 and 3 of the
second**, leaving the second's decision to segment and its points 2, 4 and
5 standing. The **fourth** superseded **the third wholly** — both its decision to
measure the source's keyframe interval and its degradation for irregular sources
— and the second's decision to segment and its points 2, 4 and 5 stood through
all of it. What follows is the surviving material from all four, arranged as one
decision, with each failed design kept as history rather than deleted.

### What was true before any of it

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

### The Range measurement

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

This measurement is the foundation everything below rests on, and it survived all
four designs unchanged. It is reproducible: `go run ./tools/rangeprobe` with an
instance URL in the environment. It reaches a live third-party CDN with a
developer's credential, so it is a tool and never a CI test.

### Design 1 — byte addressing, and the overlapping-range failure

The measurement said the upstream ranges, and the first attempt inferred from it that a
transcoded stream could be made seekable **by byte**: advertise a length, map a
requested offset onto a timestamp, restart ffmpeg there. Because a keyframe-aligned
segment can be produced with `ffmpeg -ss` for the cost of one ranged fetch,
restart-at-offset is cheap — so a file-backed cache accumulating the source once,
which is what `seanime`'s non-local path does because it assumes upstream Range
with no fallback, was declared **not** needed.

**That inference was wrong, and it was disproved live rather than argued down.**
A ranging upstream makes an offset restart *cheap*; it does not make per-range
restart *correct*. Serving each client range from its own ffmpeg was built and
tested: a media element issues overlapping, opportunistic ranges, so one playback
drew bytes from two transcodes at different timestamps and would not decode. The
file-backed design the first attempt dismissed as unnecessary is required by **the
client's** range behaviour, not by the upstream's — the two are not variations,
they are different systems, and building the wrong one is expensive. That is what
the measurement was for, and it is why the delay to take it was worth paying.

The spool architecture fixed the decode failure. One transcode per region of a
playback writes to one file and every reader sees the same bytes; **three per
ticket**, because a media element reads a head and a tail at once and a bound of
one meant the client's second region destroyed its first. Live,
`MEDIA_ERR_DECODE` is gone, `readyState` reaches 4, and seeks land on the clock.

**What the spool cannot fix is the length.** `serveSeekableRemux` advertised the
source's size and then wrote however many bytes ffmpeg actually produced. For a
remux that copies video and re-encodes only audio the two are close. For a release
that reaches ffmpeg because its video must be re-encoded they are not: a 61 GB 4K
source becoming a 1080p h264 output differs by an order of magnitude, so the
response is truncated against its own `Content-Length` and the end of the timeline
is unreachable. The estimate can be made *good* — advertising the probed source
size instead of a flat bitrate closed a factor of twenty-two — and it cannot be
made *exact*, because the true length is unknowable until the transcode finishes
and the transcode is the thing the length is supposed to describe.

A second, independent problem sat beside it. Nothing bounded how far ahead of the
viewer the transcode ran. An audio-only remux proceeds at near-copy speed, so one
click on Play writes the entire release into a spool as fast as the upstream will
serve it. Reaping an abandoned session bounds the *leak*; it does not bound the
*rate*.

### What the reference implementations do

Both answer this with HLS, and the difference between them decided which variant
applies here.

- **`remux`** generates a VOD playlist server-side listing every segment from
  zero to the end at a uniform length, so a client may seek anywhere
  immediately; each segment URL carries its own cumulative start time, and a
  request for one that does not exist restarts ffmpeg at that offset. It bounds
  itself two ways Mosaic does not: it `SIGSTOP`s the encoder when it runs 300 s
  ahead of the playhead, and deletes segments 30 s behind it.
- **`seanime`** builds an *exact* playlist from the source's real keyframe
  index, extracted with `ffprobe -show_entries packet=pts_time,flags`.

**Seanime's variant is not available to Mosaic**, and the reason is the Range
measurement above. Reading every video packet's timestamp over a remote 61 GB URL
means transferring the file — the opposite of the one ranged fetch that made
restart-at-offset affordable. Seanime's own fallback, synthetic keyframes at a
flat two seconds when extraction yields nothing, concedes the case; and seanime's
transcoder is local-file only, so it never had to face it.

### Design 2 — segmented HLS at a constant length, and the drift that was not tolerable

The **second** took the alternative the first had left open, and served the
transcoded stream as HLS with a uniform VOD playlist computed from the probed
duration at a **constant six-second** segment. It conceded, in its own decision
point 3, that where the video is copied "ffmpeg cuts at the next keyframe in the
source and real durations drift from advertised ones", and called that drift
tolerable because `#EXTINF` is advisory and `-copyts` makes the timestamps inside
the segments authoritative — Jellyfin and `remux` both live with this.

Two things were wrong with that.

**First, it is the combination `remux` refuses.** `remux` serves a computed
uniform playlist only for MPEG-TS. The moment it uses fragmented MP4 —
`use_fmp4()`, which is video-copy of an HEVC source — it abandons the computed
playlist and serves ffmpeg's own rolling `EVENT` playlist instead, upgrading it
to `VOD` only once ffmpeg has written `EXT-X-ENDLIST`. Its comment gives the
reason: fMP4 segments snap to keyframe boundaries, so the playlist must reflect
real timing. The **second** specified a computed playlist *and* fMP4, which is the
one pairing that record avoids.

**A rolling playlist is not available to Mosaic**, whatever remux does with it. A
viewer opening a two-and-a-half-hour film must see two and a half hours of
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
ten six-second segments therefore describes six ten-second ones: not a timeline
that is slightly off, a timeline whose segments do not exist at the positions it
names.

The fourth row is the trap. Asking for *more* than the real interval — ten
against 9.96 — makes ffmpeg skip a keyframe and emit a doubled first segment,
because at 9.96 the elapsed time has not yet reached the target. **The request
must be at or below the true interval, never above it.** The third row is the
other half of the answer: when the two agree, a copied stream segments exactly.

### Design 3 — measure the source's keyframe interval. Never built, wrong twice.

The **third** read those rows and concluded that the origin must **measure** the
interval rather than choose it: where video is re-encoded the origin chooses,
because `-force_key_frames` makes the playlist exact by construction; where video
is copied, a head-only probe — `ffprobe -read_intervals`, reported as returning in
**93 ms** and costing a couple of ranged fetches — would read the keyframe
timestamps of the first seconds and hand back one number, which the playlist would
advertise and which ffmpeg would be asked for a value comfortably below. The same
probe would say whether the spacing was regular, and an irregular source would be
a **named degradation** — re-encode the video, or decline to segment — rather than
a silently wrong playlist. It expected segment length to become a per-playback
value rather than a constant, a head probe joining the existing probe
([platform#29](0029-probing-and-the-per-stream-playback-decision.md)) rather than
replacing it, both cheap, both cached on the Part, the second running only when
the first said the video would be copied — and it expected `remux`'s wall to be
removed rather than routed around, its rolling playlist for fMP4 being a
consequence of never measuring the source.

**Both of its decisions were wrong, it was superseded wholly, and none of it was
built.**

**The head probe does not exist.** `-read_intervals` bounds what ffprobe
**reports**, not what it **reads**. Measured against an HTTP server counting bytes
served:

| Source | Window | Bytes read |
|---|---|---|
| MP4, `moov` at end | 20 s | **200%** of the file |
| MP4, `moov` at end | 60 s | **200%** of the file |
| MP4, `+faststart` | 20 s | **100%** of the file |
| Matroska | 20 s | **100%** of the file |

The window makes no difference. The "93 ms" was wall-clock against a small local
file and was never a measure of what crosses a network. Learning the interval
costs the same whole download as seanime's full keyframe index — the very cost
the second attempt had rejected that index for.

**And the playlist does not describe a continuous run.** This is the deeper error.
The **third** assumed a playlist must describe what one uninterrupted ffmpeg will
emit, which is why a mismatch between requested and actual segment length looked
fatal. It does not, because **the origin restarts ffmpeg at the position the
client asked for.** That machinery already existed — it is design 1's `-ss` before
`-i` with `-copyts`, whose affordability the Range measurement established — and
`remux` uses exactly it, deriving `-start_number` from the same cumulative time.

Measured, against a source with a ten-second GOP asked for six-second segments:

| Restart | Nominal start | Actual first PTS |
|---|---|---|
| `-ss 60 -start_number 10` | 60.000 | **60.000000** |
| `-ss 42 -start_number 7` | 42.000 | **40.000000** |

A segment produced by a restart lands on its nominal position, within one keyframe
interval, **and the error does not accumulate** — each seek is anchored to the
playlist's own arithmetic rather than to the previous segment. Two seconds out on
the second row is the keyframe at 40 s, which is where a copy can begin and
nowhere else.

Three records governed one slice and two were superseded. That is the cost of
reasoning where measuring was available: the third attempt was written from an inference
about ffprobe's flags and a wall-clock number that measured the wrong thing.

## Decision

**The origin stays a byte relay wherever it can, and is a pipe only for streams
ffmpeg must produce. Where it must produce one it serves HLS over a nominal
segment grid, and segment *N* is defined as what ffmpeg produces when started at
*N* × the segment length — not the *N*th segment of a continuous run.**

### The relayed path

1. **The relayed path is seekable and needs nothing built.** `Handler` already
   forwards `Range` and `If-Range` and relays `Content-Range`, `Accept-Ranges`
   and the `206`. The measurement supplies the missing half of the claim.

2. **Direct play is untouched.** A release needing no work is still relayed
   byte-for-byte with `Range` passed through, which was always seekable and
   remains the cheapest path. HLS applies only to the streams ffmpeg produces.

### The segmented path

3. **The playlist is a nominal grid** computed from the probed duration and a
   segment length **the origin chooses**. It stays complete and stays `VOD` —
   every segment listed before a byte is produced, `#EXT-X-PLAYLIST-TYPE:VOD`
   and a closing `#EXT-X-ENDLIST` — so the whole running time is scrubbable from
   the first frame. This is precisely the property the advertised
   `Content-Length` was synthesising, obtained honestly: a duration the probe
   measured, rather than a size nobody can know.

4. **A segment request is a seek, and the restart machinery is reused.** Serving
   segment *N* means having a transcode whose output covers it; otherwise one is
   started at *N* × the segment length, with `-ss` before `-i`, `-copyts` and
   `-start_number N`. Those are design 1's flags and its arithmetic, re-keyed
   from a byte offset to a segment index, and they stay affordable for exactly
   the reason the Range measurement gives.

5. **The origin does not measure the source.** There is nothing to measure that
   can be afforded, and nothing that needs measuring: the grid is nominal by
   construction. Where the video is re-encoded the origin's own
   `-force_key_frames` puts a keyframe on every boundary anyway, so the playlist
   is exact by construction at whatever interval the origin prefers.

6. **Seek accuracy is one keyframe interval, and that is the honest bound.** A
   copy can only begin at a keyframe, so a viewer asking for 42 s gets 40 s. This
   is what every server in this family does and what a viewer experiences as a
   seek landing correctly.

7. **Production is bounded ahead of the playhead and segments are evicted behind
   it.** This is not an optimisation to add later; it is the half of the design
   that makes the storage finite, and it is what a single spool per playback
   structurally cannot have, because a monolithic file offers no unit to evict.

8. **Segments are fragmented MP4 rather than MPEG-TS.** The origin already emits
   fMP4, the client profile accepts HEVC, and Apple's HLS authoring rules require
   fMP4 for HEVC — MPEG-TS cannot carry it correctly.

9. **The web client adopts a media framework. This is
   [web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md)'s
   condition firing, not
   [web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md)
   being reversed.** That record chose a bare `<video>` element and said in terms
   that the client "adopts a media framework when the Platform serves something a
   `<video>` element cannot play — which today means HLS". Safari plays HLS
   natively; every other browser needs the library.

### What the container does not decide

10. **Matroska is not the boundary, because the player is not MSE.** The Shell
    renders a bare `<video src>`
    ([web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md)),
    whose native demuxer handles Matroska; the client profile declares
    `containers: []` deliberately and says why. Nothing in the per-stream decision
    consults the container at all — `Plan.DirectPlay` is
    `Video == Copy && Audio == Copy` — and `ShouldRemux`, which encodes the
    container rule, was called from nowhere in production.

    So the set that cannot be seeked is not a container subset. It is the set of
    releases that reach ffmpeg *for any reason*: an undecodable video codec, HDR
    needing a tone-map, or audio the client cannot decode. That set is larger than
    "Matroska" in some directions and much smaller in others — a 1080p h264+AAC
    MKV direct-plays today and always did.

11. **The output container is a second constraint on the audio decision.** The
    client profile answers "can this be decoded"; the muxer answers "can this be
    carried". Only the first was being asked, and the two disagree: Chrome decodes
    FLAC and fragmented MP4 refuses it. The constraint binds only once ffmpeg is
    already involved — while a stream is relayed the container is the source's
    own.

12. **A failed transform is not a successful empty stream.** The origin must know
    it has bytes before it writes a status.

## Alternatives considered

**Assume the upstream ranges and build accordingly.** *Rejected*, and the run
exists partly to say why it was worth the delay. The two candidate designs differ
in their storage model, their failure modes and their cost; the assumption was
load-bearing and cheap to check.

**Cap selection at the encoder's ceiling.** *Rejected.* A declared display height
is a true statement about the panel and the right input to *selection*; using it
as the encode ceiling asked this Platform for a real-time 4K tone-mapped encode.
Capping selection instead would hand a 4K display a 1080p release to upscale. The
two caps are separate numbers answering separate questions.

**Serve HLS to a hls.js client.** *Left open by design 1, and then taken.* It is
the conventional answer and it needs a client library, which
[web#5](https://github.com/mosaic-media/web/blob/main/docs/adr/0005-the-web-player-is-the-browser.md)
deliberately did not have; it was recorded as the fallback if segmenting a bare
`<video>` proved unworkable, and as a client decision rather than an origin one.
Byte addressing then proved unworkable, and this is what the origin serves.

**Keep byte addressing and bound the spool.** *Rejected.* It is the smaller change
and it leaves the defect: the advertised length is structurally an estimate, so a
re-encoded release still truncates against its own `Content-Length`. "Resume that
works only on some releases is not resume" is the requirement M3 was written
against, and this alternative satisfies it for the copy case only.

**Extract a real keyframe index, as `seanime` does.** *Rejected.* Over a remote
source it costs the whole file, which is the cost the Range measurement exists to
avoid.

**Measure the source's keyframe interval from the head of the file and match it.**
*Superseded* — this was design 3, and it was argued at the time as being *not*
seanime's index, because seanime reads every video packet in the file to learn
where every keyframe is while this reads the head and learns one number. Measured,
the distinction does not exist: `-read_intervals` transfers the whole file
regardless of the window. It is unaffordable, and unnecessary once the grid is
understood as nominal.

**Tolerate the mismatch between requested and actual segment length, as `remux`
does for MPEG-TS.** *Rejected.* The measurement shows it is not a tolerance
question: within one continuous run the segments are not near the positions the
playlist names, they are at entirely different ones. The nominal grid does not
tolerate that mismatch either — it prevents it, by making every segment its own
restart.

**Serve ffmpeg's own rolling playlist**, as `remux` does for its fMP4 case.
*Rejected, twice, on the same ground.* It is accurate about segment durations, and
it makes a film a live stream until the transcode finishes: the scrubber grows as
the encoder advances, so a viewer cannot resume to ninety minutes until the
encoder has reached ninety minutes, which is the requirement this milestone exists
for.

**Re-encode the video whenever we segment, so the boundaries are always ours.**
*Rejected as the default, twice.* Design 3 kept it as the fallback for an
irregular source, and that fallback lapsed with the degradation it served. It is
correct, and it is the most expensive thing the system can do — a 4K HEVC source
measured around 30× slower than realtime — so making it the price of seeking would
make seeking cost more than the release it is seeking in, and it would make the
expensive path the only path. It also buys exactness the nominal grid already
delivers to within one keyframe; paying that for two seconds of seek precision is
the wrong trade.

**Pick a segment length short enough to divide any keyframe interval.** *Rejected*,
because no such length exists: the cut still lands on the next keyframe whatever
is asked for, so a one-second request against a ten-second interval still produces
ten-second segments. Asking for less does not make them smaller; it only stops
them being doubled.

**Decline to segment a source whose keyframes are irregular, and name the
degradation.** *Superseded with design 3.* It was the honest answer while the
playlist was believed to describe a continuous run — better to say "this release
cannot be seeked while transcoding" than to send a viewer to a position that does
not exist — and it was expected to be the case that produced support questions.
Under a nominal grid there is nothing to decline: a scene-cut source segments as
unevenly as any other and seeks just as well.

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

- **The seekable set is stated in terms of work, not containers**, which is what
  the code always did and the documents did not.
- **The byte-addressing code is deleted, not left beside its replacement**:
  `contentLength`, `offsetAt`, `parseByteRangeStart` and `serveSeekableRemux`,
  with their tests. `ShouldRemux` — the dead code design 1 named and deliberately
  did not delete, leaving it in place because deleting it was a decision about
  the container heuristic's future rather than about the measurement — went in the
  same change, because the heuristic it encodes has no caller in either design.
- **`Spool` survives and becomes what it was shaped for.** It is already a port
  with a substitutable factory; segmentation supplies the bounded working set that
  lets the implementation be memory rather than a file. That is the intended end
  state and the size is an honest constraint rather than a free win: a copied 4K
  video stream is around 8 MB/s, so a window measured in minutes is measured in
  hundreds of megabytes.
- **A linear play triggers periodic restarts**, and this is the cost being
  accepted. Where segments run longer than nominal, a continuous run reaches the
  end of the release having produced fewer segments than the playlist names, so
  the client eventually asks for one that run will never emit and the origin
  restarts. Bounded, self-correcting, and the reason a "too far ahead" threshold
  is needed rather than optional.
- **`-copyts` is load-bearing rather than merely useful.** It is what makes a
  restarted segment carry the timestamps its nominal position implies; without it
  every restart would claim to begin at zero.
- **The segment length is a free choice**, so it is chosen for the costs it
  balances — restart granularity against per-segment overhead — rather than
  dictated by a source nobody can afford to inspect. There is no keyframe probe
  and no irregular-source degradation, so nothing becomes a per-playback measured
  value and nothing joins
  [platform#29](0029-probing-and-the-per-stream-playback-decision.md)'s probe.
- **`remux`'s wall is removed rather than routed around**, though not the way
  design 3 expected. Its rolling playlist for fMP4 exists because a continuous run
  cannot be described by a computed playlist; once a segment is a restart rather
  than the *N*th output of one run, a computed playlist and fragmented MP4 are
  compatible and the whole running time stays scrubbable.
- **A client that cannot run the framework loses transcoded playback**, where
  before it got an unseekable stream. That is a real loss and it is the price of
  the decision; the relayed path still works for every release needing no
  transform, and no client other than the Shell exists to be affected yet.
- **`MimeType` on the `Player` node is load-bearing.** It exists and is already
  set for a remuxed stream; it is what tells a client which pipeline to choose
  before it fetches anything.
- **Resume becomes exact on a transcoded stream**, which is the release
  requirement this milestone exists to satisfy, and the reason "seek and resume on
  a remuxed stream" was listed as owed rather than deferred.
- **A slower-than-realtime encode is not a seeking problem and segmenting will not
  fix it.** The release measured for Range runs about 30× slower than realtime
  even bounded to 1080p, because decoding 4K 10-bit HEVC in software is the floor.
  Segmenting makes a stream seekable; it does not make it arrive.
- **The honest test of the segmented path is a release needing only an audio
  encode**, which remuxes at near-copy speed.
- **The measurements are kept in this record so the next reader does not repeat
  them.** Four designs in four days, three of them wrong, and every one of the
  three was found wrong by measuring something the record before it had reasoned
  about — the Range behaviour of a real CDN, what a media element does with
  ranges, what ffmpeg does with `-hls_time` against a copied stream, and what
  `-read_intervals` actually reads.
