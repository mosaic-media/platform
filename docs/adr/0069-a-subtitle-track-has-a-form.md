# A subtitle track has a form, and only one form can be burned into the picture

**Status:** Accepted. **Built.** The classification, the three delivery paths and
the opt-in setting are in. The picture-overlay path is verified as a filtergraph
and not against a real PGS stream — see Consequences. **Partly superseded:** the
typeset track's two fates became three under
[platform#70](0070-a-styled-subtitle-goes-to-the-client.md), which builds the
client-side renderer this record rejected as blocked, and demotes burning to the
answer for when it cannot be used. The classification and the graphic path stand.

**Date:** 2026-07-31

## Context

[platform#68](0068-subtitles-are-a-rendition.md) delivers subtitles as HLS
renditions and offers **every** embedded track as one. That is wrong for two of
the three kinds of subtitle track a release can carry, and it is wrong in
different ways.

**Measured, on the origin's own ffmpeg:**

- **SubRip and its relatives** convert to WebVTT losslessly. There is nothing in
  them a rendition cannot carry.
- **ASS and SSA** — what anime releases use — carry *where the words go and what
  they look like*. A cue authored `{\pos(640,120)\c&H00FF00&\fs72}` over a
  doorway arrives through a WebVTT rendition as ordinary bold text at the bottom
  of the screen. The words survive; the position, the colour, the size and the
  alignment do not. That is the difference between a sign appearing over the sign
  and a line of text under the picture, and for a release that typesets its signs
  it is most of what the subtitles were for.
- **PGS, VobSub and DVB** are pictures. There is no text in them at all. ffmpeg
  refuses: *"subtitle encoding currently only possible from text to text or
  bitmap to bitmap"*. Offered as a rendition anyway, the extraction fails, the
  origin answers 200 with an empty document, and the player lists a subtitle
  track that draws nothing for the length of the film. **That shipped.**

`remux` is the reference here and it makes the same split, which is worth
recording accurately because it is easy to remember wrongly: `remux` burns in
**picture** subtitles, compositing them with `overlay` in a `filter_complex`
(`transcode/engine.rs`). It has no text burn-in at all — text tracks it extracts
to a cache and serves as sidecar files.

## Decision

**A subtitle track's codec decides how it can be delivered, and burning it into
the picture is the last resort — priced, opt-in where there is a choice, and
automatic only where there is none.**

Three forms, three answers:

| Form | Codecs | Delivery |
|---|---|---|
| Plain | `subrip`, `mov_text`, `webvtt`, unknown | Rendition. Faithful, free. |
| Typeset | `ass`, `ssa` | Rendition by default, flattened. Burned if the viewer asked for it as authored. |
| Graphic | `hdmv_pgs_subtitle`, `dvd_subtitle`, `dvb_subtitle`, `xsub` | Burned, or nothing. Never offered as a rendition. |

1. **A graphic track is never listed as a rendition.** It cannot become one, and
   listing it produces a menu entry that silently draws nothing — the worst of
   the available failures, because it looks like it worked.

2. **A graphic track is burned when it is the one the preference chose**, and
   only then. A Blu-ray whose only English subtitles are pictures must still be
   able to answer somebody who asked for English, and there is no other way to
   answer them.

3. **A typeset track is flattened by default and burned on request.** The
   flattened rendition is free; the burn costs a video encode. So the choice is
   a per-viewer setting — `typeset` on the language preference from
   [platform#67](0067-language-is-a-persons-preference.md) — **off by default**, and
   the control says what it costs rather than hiding it.

4. **Asking for typeset fidelity never burns a plain track.** SubRip has no
   styling an encode could preserve, so there would be nothing to buy with it.

5. **`off` never burns**, and this is the strongest form of [platform#67](0067-language-is-a-persons-preference.md)'s rule.
   Burning is irreversible for the playback, so doing it to somebody who asked
   for no subtitles would put text on their screen that they cannot turn off.

6. **Nothing is offered beside a burned track.** It is in the picture already,
   and listing it as a rendition too would draw it twice.

The two burn paths differ, and the difference is a real cost:

- **Picture tracks are composited from the stream this run already has open** —
  `[0:N]scale[sub];[0:V]…[main];[main][sub]overlay=eof_action=pass:repeatlast=0[v]`.
  No filename, no escaping, no second read of the source.
- **Text tracks are drawn by libass**, which can only read a *file*. So the
  source URL is named again inside the filtergraph and opened a second time.
  There is no way around it: the filter cannot be handed a stream that is already
  open.

## Alternatives

**Send the ASS to the client and render it there with libass-wasm.** *Rejected
for now, and it is the design that would be best.* It preserves everything, costs
no encode, and JASSUB does it in a browser today. It needs the client to be handed
an ASS URL, which means a `subtitles` prop on the `Player` component — a
native-vocabulary growth in `contracts` with a `@mosaic-media/sdui-react` bump —
and the publish train has been returning 403 on tag pushes since before this work
began. This decision is what can be built now; that one supersedes it when it can.

**Burn everything, always.** *Rejected.* It is simpler and it is what a naive
reading of "subtitles that appear over stuff" asks for. It also forces the most
expensive operation the Platform has onto every release with a subtitle track,
including the ones where a free rendition would have been identical.

**Never burn; flatten what can be flattened and drop the rest.** *Rejected.* It
makes a Blu-ray's only English subtitles unreachable, and it makes typeset signs
permanently unavailable rather than merely expensive.

**Convert ASS to WebVTT with positioning cues.** *Rejected, though tempting.*
WebVTT does have `line`, `position` and `align`, so a fraction of ASS would
survive a careful mapping — but not `\pos` at arbitrary coordinates, not colours,
not fonts, not the layered effects typesetting actually uses. It would produce
subtitles that are wrong in a new and harder-to-explain way, and it would still
need the burn for everything it could not express.

## Consequences

- **Subtitles can now turn a direct-play into a transcode.** A release that would
  have been relayed untouched is re-encoded if the viewer asked for typeset
  fidelity and the track is ASS. This is the one place where a *preference* moves
  a release across the cheap/expensive line, and it is why the setting is opt-in
  and says so. `subtitle_burned` is on the play telemetry for the same reason:
  from the outside this presents only as a release that suddenly plays badly.
- **A burned track cannot be switched off.** By the time it reaches the client it
  is part of the picture, so the player's subtitle menu is empty for that
  playback. A rendition is switchable and a burn is not, and that asymmetry is
  the strongest argument for the client-side libass path above.
- **The libass path reads the source twice.** Once for the frames and once for
  the filter's own demuxer. Confirmed in the access log of a local server while
  measuring the escaping.
- **Filtergraph values are escaped twice, and getting it wrong fails silently.**
  A filtergraph is unescaped once to split filters from options and again per
  option value, so `https://host` must reach ffmpeg as `https\\://host`.
  Single-escaped, ffmpeg read `//host` as an unrelated option, reported "unable to
  parse option value as image size", **then encoded the video with no subtitles on
  it and exited successfully**. That is the third measured ffmpeg flag in this
  area that reads correctly and behaves wrongly, after `-read_intervals`
  ([platform#66](0066-the-playlist-is-a-nominal-grid.md)) and `-t` ([platform#68](0068-subtitles-are-a-rendition.md)).
- **`-copyts` is now load-bearing for burn-in, not only for seeking.** The
  `subtitles` filter reads the source independently and emits cues on the
  source's own clock. Measured: with `-ss 12 -copyts` a cue authored at 14 s
  burned at 14.0–17.8 s, matching the unseeked control exactly; **without
  `-copyts` it did not appear at all**, because the frames had been rebased to
  zero and the cues had not.
- **The picture path is verified as a graph and not against real PGS.** ffmpeg
  cannot encode text subtitles into a bitmap format, so no fixture could be built
  here; the filtergraph was exercised with a stand-in overlay stream and accepted.
  What is unverified is PGS decoding and palette handling, which a real Blu-ray
  release will settle.
