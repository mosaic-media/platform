# Subtitles are a rendition, extracted a window at a time

**Status:** Accepted. **Built for embedded tracks on the transcoded path.** Not
built for direct-played releases or for module-provided subtitles; both are named
under Consequences. **Partly superseded:** offering *every* embedded track as a
rendition was wrong for picture tracks, which cannot become one, and lossy for
typeset ones — corrected by
[platform#69](0069-a-subtitle-track-has-a-form.md). The rest stands.

**Date:** 2026-07-31

## Context

[platform#67](0067-language-is-a-persons-preference.md) decided *what* a viewer
should read and left *how it reaches them* open, which is why it is built in part
and not built: the escalation is computed on every play, recorded in telemetry,
and rendered nowhere. `off`, `forced` and `full` are indistinguishable on screen.

Three facts bound the answer.

**The origin drops subtitles today, on purpose.** `Plan.ffmpegArgs` ends with
`-sn`, and the reason in the comment is correct: an MKV's subtitles are usually
SubRip or ASS, neither maps into MP4, and asking ffmpeg to copy them fails the
whole command rather than just the subtitle stream. So they cannot travel in the
muxed output, and this is not a flag to remove.

**A sidecar file would cost a client release.** Attaching a subtitle file to the
player means a `subtitles` prop on the `Player` component — a growth of the
native vocabulary, specced in `contracts`, generated into Go and TypeScript,
carried by a `@mosaic-media/sdui-react` bump. That is the sanctioned second
answer under the Platform's screen rule, and it is not free.

**Subtitle packets are interleaved with video packets.** There is no way to read
a release's text without reading past its pictures. Extracting the whole track up
front costs a full read of the container before the first cue appears — on a
remote 15 GB release over a debrid link, minutes of the viewer's bandwidth
competing with the playback it is meant to accompany.

## Decision

**Subtitles are delivered as HLS subtitle renditions, extracted from the source
one window at a time, and nothing is written to disk.**

1. **The player is told through HLS, not through the SDUI.** The origin already
   serves a playlist to every transcoded release, and every client that can play
   it — hls.js, and Safari natively — already has a subtitle menu, a track
   selector and a WebVTT renderer. Declaring an `EXT-X-MEDIA:TYPE=SUBTITLES`
   rendition reaches all of that with **no contract change and no client
   release**. This is the screen rule's first answer arriving from an unexpected
   direction: the vocabulary does not have to grow, because the thing being
   described is not a component.

2. **The entry point becomes a master only when there is something to declare.**
   `index.m3u8` stays the URL a client is given. It holds the video's segment
   list when the release has no subtitles — byte for byte what it served before
   — and a master naming the video rendition plus one subtitle rendition per
   track when it has. `CODECS` stays out of the master for the reason
   [platform#64](0064-the-transcoded-stream-is-segmented.md) gave for having no
   master at all: the origin cannot know it before the transcode runs, and a
   wrong one makes a browser refuse a stream it could have played.

3. **Every track is offered; at most one is default.** [platform#67](0067-language-is-a-persons-preference.md)'s rule — *a
   language nobody asked for is never selected* — is about selection. The
   rendition list carries every embedded track so the player's own menu works,
   and `DEFAULT=YES` marks the one the preference and its escalation chose, or
   none. That is also the only part of a track picker (M3 item 6) this delivers,
   and it delivers it by not building one.

4. **A subtitle rendition has its own grid: sixty seconds, ten times the
   video's.** HLS requires renditions to describe the same running time, not to
   divide it the same way. A subtitle segment costs one ffmpeg run and one range
   read over the container, so cutting on the video's six-second grid would pay
   that ten times as often for the same bytes. A minute is also more than any
   player reads ahead.

5. **The window is bounded by `-to`, not `-t`.** `-t` bounds the output's
   *duration*, and `-copyts` has already rebased the output onto the source's
   clock — so `-ss 60 … -t 60` stops the instant it starts. Measured against a
   file with a cue every ten seconds: window 0 yielded six cues, window 1 yielded
   none. As a shipped bug that is every subtitle in a film vanishing one minute
   in, and it is recorded here because it is the second time this repository has
   been caught by an ffmpeg flag that reads correctly and measures wrong.

6. **Nothing is spooled.** A window of dialogue is a few hundred bytes and goes
   straight to the response. The segment spool exists because a video segment
   must be complete before it is served; a WebVTT document has no such
   constraint, and inventing a spool for it would add a reaper, a lifetime and a
   disk budget to something smaller than the HTTP headers around it.

## Alternatives

**A sidecar `<track>` on the player, via a `Player` subtitles prop.**
*Rejected for now, not wrong.* It is the only design that also covers
direct-played releases, and it is the natural follow-up. It was not chosen first
because it costs a contract change, a generated-code regeneration and a client
release to deliver what HLS already delivers for free on the path that needs it
most — and because the `contracts` publish train is not currently movable.

**Extract the whole track up front and serve it as one segment.** *Rejected.* It
is simpler and it is the design that reads best on paper. It also means reading
the entire container before the first cue can be shown, which on a remote release
is minutes of bandwidth taken from the playback it is meant to accompany, at
exactly the moment a viewer is waiting for a first frame.

**Segment the subtitles in the same ffmpeg run as the video.** *Rejected on
measurement*, and it was the preferred design going in, because it would cost no
extra read at all. ffmpeg 5.1 will not do it: `-var_stream_map` with an `sgroup`
refuses to combine WebVTT with `-hls_segment_type fmp4`, a second `-f hls` output
for the subtitle stream defaults to an mpegts segment type that cannot carry
WebVTT, and `-f segment -segment_format webvtt` cuts on packet boundaries rather
than on a time grid — a 60-second source with a cue every 20 seconds produced
three segments, not ten. All three were tried.

**Burn subtitles into the video.** *Rejected.* It forces a video encode on a
release that may not otherwise need one, which is the single most expensive thing
the Platform can do, and it makes the choice unchangeable for the rest of the
playback.

## Consequences

- **Direct-played releases get no subtitles.** A relayed stream is the upstream's
  own bytes and the origin adds no playlist to hang a rendition off. This is the
  gap the sidecar alternative closes, and it is real: a release that needs no
  transcoding is the common and the wanted case.
- **The extra read is the cost, and it is proportional rather than up front.**
  Playing with subtitles on reads the container roughly twice — once for the
  video segments and once for the subtitle windows — because the two are separate
  processes and there is no way to reach the text without the pictures.
- **Module-provided subtitles are unaffected and still unreached.** A `subtitles`
  capability returns a URL to a file, which needs no extraction at all and would
  be far cheaper than this. Nothing calls it yet; that is roadmap item 5's own
  gap, and this record neither closes nor blocks it.
- **`v1.Subtitle` still cannot say a track is forced**, so [platform#67](0067-language-is-a-persons-preference.md)'s forced
  behaviour remains complete for embedded tracks and unavailable for
  module-provided ones, exactly as that record said.
- **One claim here is unverified and it is a client-side one.** The origin's two
  clocks were measured to agree: with `-copyts`, video segment 10 of a
  six-second grid begins at PTS 55.023 — one GOP early, which
  [platform#66](0066-the-playlist-is-a-nominal-grid.md) allows — and the cue for
  60 s reads 60.023, the same clock with the same container start offset.
  Whether hls.js maps a raw WebVTT segment onto an fMP4 timeline without an
  `X-TIMESTAMP-MAP` header cannot be measured without a browser. No header is
  injected, because a mapping written blind is worse than none. If subtitles land
  at a constant offset, that is the cause and it is one constant to fix.
