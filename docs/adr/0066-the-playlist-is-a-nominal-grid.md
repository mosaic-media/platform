# The playlist is a nominal grid, and a segment index is a seek instruction

**Status:** Accepted. **Built, and never played.** The nominal grid is computed
in `internal/transport/playback/playlist.go` and the restart-at-`-ss` in
`segments.go`; no release has been watched through it. See
[platform#64](0064-the-transcoded-stream-is-segmented.md)'s status line.
Supersedes [platform#65](0065-the-segment-length-is-measured.md) wholly — both its
decision to measure the source's keyframe interval and its degradation for
irregular sources. [platform#64](0064-the-transcoded-stream-is-segmented.md)'s
decision to segment, and its points 2, 4 and 5, stand.
**Date:** 2026-07-30

## Context

[platform#65](0065-the-segment-length-is-measured.md) reasoned from one correct
observation to two wrong conclusions.

The observation stands: a copied stream is cut at the source's keyframes and
nowhere else, so asking for six-second segments of a release with ten-second
keyframes produces **ten**-second segments. From that it concluded that the
origin must learn the interval — cheaply, from the head of the file — and must
decline to segment a source whose keyframes are irregular.

**Both conclusions are wrong, and the second one is wrong in a way that matters
more.**

### The head probe does not exist

`-read_intervals` bounds what ffprobe **reports**, not what it **reads**.
Measured against an HTTP server counting bytes served:

| Source | Window | Bytes read |
|---|---|---|
| MP4, `moov` at end | 20 s | **200%** of the file |
| MP4, `moov` at end | 60 s | **200%** of the file |
| MP4, `+faststart` | 20 s | **100%** of the file |
| Matroska | 20 s | **100%** of the file |

The window makes no difference. [platform#65](0065-the-segment-length-is-measured.md) cites "93 ms" as evidence the probe is
affordable; that was wall-clock against a small local file and was never a
measure of what crosses a network. Learning the interval costs the same whole
download as seanime's full keyframe index — the very cost that record rejected.

### The playlist does not describe a continuous run

This is the real error. [platform#65](0065-the-segment-length-is-measured.md) assumed a playlist must describe what one
uninterrupted ffmpeg will emit, which is why a mismatch between requested and
actual segment length looked fatal.

It does not, because **the origin restarts ffmpeg at the position the client
asked for.** That machinery already exists — it is
[platform#63](0063-the-origin-is-a-pipe-only-where-it-must-be.md)'s `-ss` before
`-i` with `-copyts`, whose affordability that record measured — and `remux` uses
exactly it, deriving `-start_number` from the same cumulative time.

Measured, against a source with a ten-second GOP asked for six-second segments:

| Restart | Nominal start | Actual first PTS |
|---|---|---|
| `-ss 60 -start_number 10` | 60.000 | **60.000000** |
| `-ss 42 -start_number 7` | 42.000 | **40.000000** |

A segment produced by a restart lands on its nominal position, within one
keyframe interval, **and the error does not accumulate** — each seek is anchored
to the playlist's own arithmetic rather than to the previous segment. Two
seconds out on the second row is the keyframe at 40 s, which is where a copy can
begin and nowhere else.

## Decision

**Segment *N* is defined as what ffmpeg produces when started at *N* × the
segment length. It is not the *N*th segment of a continuous run.**

1. **The playlist is a nominal grid** computed from the probed duration and a
   segment length the origin chooses. It stays complete and stays `VOD`, so the
   whole running time is scrubbable from the first frame — [platform#64](0064-the-transcoded-stream-is-segmented.md)'s point that
   this record does not touch.

2. **A segment request is a seek.** Serving segment *N* means having a transcode
   whose output covers it; otherwise one is started at *N* × length with `-ss`
   before `-i`, `-copyts`, and `-start_number N`.

3. **The origin chooses the segment length, and does not measure the source.**
   There is nothing to measure that can be afforded, and nothing that needs
   measuring: the grid is nominal by construction.

4. **Seek accuracy is one keyframe interval, and that is the honest bound.**
   A copy can only begin at a keyframe, so a viewer asking for 42 s gets 40 s.
   This is what every server in this family does and what a viewer experiences as
   a seek landing correctly.

## Alternatives

**Measure the interval and match it** ([platform#65](0065-the-segment-length-is-measured.md)).
*Superseded.* Unaffordable, and unnecessary once the grid is understood as
nominal.

**Re-encode the video whenever segmenting, so the boundaries are always ours.**
*Rejected.* It buys exactness the nominal grid already delivers to within a
keyframe, and it costs the cheap path: an audio-only remux runs at near-copy
speed, and a 4K HEVC re-encode was measured at roughly 30× slower than realtime.
Paying that for two seconds of seek precision is the wrong trade, and it would
make the expensive path the only path.

**Serve ffmpeg's own rolling playlist for copied video**, as `remux` does for its
fMP4 case. *Rejected.* Accurate segment durations, and a film becomes a live
stream whose scrubber grows as the encoder advances — so a viewer cannot resume
to ninety minutes until the encoder has reached ninety minutes, which is the
requirement this milestone exists for.

## Consequences

- **No keyframe probe, and no irregular-source degradation.** A scene-cut source
  segments as unevenly as any other and seeks just as well, because every seek
  re-anchors.
- **A linear play triggers periodic restarts**, and this is the cost being
  accepted. Where segments run longer than nominal, a continuous run reaches the
  end of the release having produced fewer segments than the playlist names, so
  the client eventually asks for one that run will never emit and the origin
  restarts. Bounded, self-correcting, and the reason a "too far ahead" threshold
  is needed rather than optional.
- **`-copyts` becomes load-bearing rather than merely useful.** It is what makes
  a restarted segment carry the timestamps its nominal position implies; without
  it every restart would claim to begin at zero.
- **The segment length is a free choice again**, so it is chosen for the costs it
  balances — restart granularity against per-segment overhead — rather than
  dictated by a source nobody can afford to inspect.
- **Three records now govern one slice, and two are superseded.** That is the
  cost of reasoning where measuring was available: [platform#65](0065-the-segment-length-is-measured.md) was written from an
  inference about ffprobe's flags and a wall-clock number that measured the wrong
  thing. The measurements are in this record so the next reader does not repeat
  them.
