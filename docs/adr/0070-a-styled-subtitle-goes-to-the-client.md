# A styled subtitle track goes to the client whole, and burning is what is left when it cannot

**Status:** Accepted. **Built**, and one step short of reachable: the Platform
emits the prop through `ui.Prop` rather than the generated `ui.SubtitleTracks`,
because the contract carrying it is committed and untagged. See Consequences.

**Date:** 2026-08-01

## Context

[platform#69](0069-a-subtitle-track-has-a-form.md) gave a styled subtitle track two
possible fates and said the better one was blocked:

> **Send the ASS to the client and render it there with libass-wasm.** *Rejected
> for now, and it is the design that would be best.* It preserves everything,
> costs no encode … This decision is what can be built now; that one supersedes
> it when it can.

The two it shipped are both lossy in one direction or the other. Flattening an
ASS track into a WebVTT rendition keeps the words and loses the positions, the
colours, the sizes and the alignment — measured: a cue authored
`{\pos(640,120)\c&H00FF00&\fs72}` over a doorway arrived as ordinary bold text at
the bottom of the screen. Burning keeps all of it and forces a video encode on a
release that may otherwise have been copied through untouched, and cannot be
switched off once it has started.

The third answer preserves everything *and* costs no encode, and the only reason
it was not built is that the `Player` primitive had nowhere to name the tracks.
That was called blocked because the Platform requires `contracts` at a published
version with no `replace`, and tag pushes to the organisation have been returning
403 since before any of this work began.

**Re-examined, the block is narrower than it looked.** It stops the *tag*, not
the decision, not the spec, and not the client. A prop can be specced and
generated; a client can implement it; and a producer can emit a prop the
generated builder in its own pinned version does not yet have.

## Decision

**A styled subtitle track is sent to the client as the script it was authored as,
and the client draws it. Flattening and burning remain, as the two answers for
when it cannot.**

1. **`Player` gains `subtitleTracks`** — a list of `{src, format, language,
   label, default}`, specced in `contracts` with `SubtitleTracks` sugar generated
   into Go and TypeScript. Named `SubtitleTracks` rather than `Subtitles`
   deliberately: `Subtitle` is an existing prop on a different tier, and a
   builder one letter from another is how `ui.Subtitle` came to be set on a
   `Stack` that has no subtitle and drew nothing for the life of a screen.

2. **The scripts ride *beside* the HLS renditions, never instead of them.** This
   is what makes the whole thing safe to ship before any client implements it,
   and it is the design rather than a courtesy: a client that cannot draw a
   script ignores the prop and uses the flattened rendition the playlist already
   declares. There is no negotiation, no capability flag and nothing for the
   Platform to get wrong about what a client can do.

3. **The viewer's choice becomes three-valued** — `plain`, `client`, `burn` —
   replacing [platform#69](0069-a-subtitle-track-has-a-form.md)'s boolean, whose `true` meant `burn`. **`client` is the
   default, because it dominates**: it preserves everything, costs no encode, and
   degrades on its own. A document written before this field says `typeset: true`
   and still resolves to `burn`, so nobody's playback changes under them.

4. **The client echoes the server's choice and does not rank.** It draws the
   track marked `default`, else the first offered. Which subtitles a person gets
   was decided by the Platform against the release
   ([platform#67](0067-language-is-a-persons-preference.md)); re-deciding it in a
   renderer would give two clients two different answers from one preference.

5. **Every client-side failure is silent.** A browser too old for the WASM, a
   blocked asset, a script that will not parse — each leaves a player that still
   has subtitles, so none is worth reporting as a playback error.

## Alternatives

**Wait for the tag.** *Rejected.* It is the tidy answer and it makes the work
wait on something with no known resolution and no owner in this session. The
spec, the generated builders and the client are all landable now, and the
producer's one line is a documented swap rather than a design question.

**Declare ASS support on `ClientProfile` and let the Platform decide.**
*Rejected*, and it was the first design. It needs a protobuf field — blocked on
the same tag — and it buys nothing: offering both forms lets the client decide by
whether it can, which is more accurate than any declaration and cannot go stale.

**Convert ASS to WebVTT with positioning cues.** *Rejected again*, for the reason
[platform#69](0069-a-subtitle-track-has-a-form.md) gave: WebVTT has `line`, `position` and `align`, and none of `\pos` at
arbitrary coordinates, colours, fonts or layered effects. It produces subtitles
wrong in a new and harder-to-explain way.

**Ship the script by extending the HLS master with a non-WebVTT rendition.**
*Rejected.* HLS subtitle renditions are WebVTT; a player handed anything else
there fails rather than ignoring it, which is the opposite of the degradation
this design rests on.

## Consequences

- **Extraction costs one read of the container, and it cannot be windowed.** An
  ASS script is one document — a header, a style table, then the events — and
  libass needs all of it before it draws a line. So the origin extracts with no
  `-ss` and no `-copyts`, and the script arrives while playback proceeds. That is
  the same read burning would have done, minus the encode.
- **The Platform emits the prop with `ui.Prop("subtitleTracks", …)`, not the
  generated builder.** The builder exists — in the spec, and in `contracts`'
  `main` — and not in the version the Platform compiles against, because that
  requires a tag. This is the narrow case the "author with the generated
  builders" rule allows ("a type the spec does not cover yet — and then add it to
  the spec"), and the reason the rule exists does not apply: the prop is not a
  string nobody renders, since `@mosaic-media/sdui-react` draws it. **It is still
  one line owed**, and it is on the roadmap rather than left to be noticed.
- **`sdui-react` gains a dependency and both apps gain a build setting.** jassub
  is dynamically imported, so a playback with no styled track pays nothing for
  it. Vite's worker format moves to ES in the Shell and the storybook, because
  that worker code-splits and Rollup refuses to emit a split build as IIFE.
- **No asset paths are named from Mosaic's side.** jassub locates its own worker,
  its two WASM builds and its fallback font relative to its own module, which is
  the one form a bundler rewrites into a built asset address. Two attempts at
  naming them failed first — a `?url` import Rollup cannot resolve out of a
  pre-built dependency, then hardcoded paths that had moved between versions —
  and the build now emits all five assets.
- **Burning is now genuinely last.** It is reached only by a graphic track, which
  has no other delivery, or by a viewer who chose it — which is the right shape
  for a machine where a re-encode may be the difference between a release that
  plays and one that does not.
- **Whether it draws correctly is unverified here.** The build emits the assets
  and the extraction preserves every tag (`\pos`, `\c`, `\fs`, the style table,
  measured), but no browser ran in this session. A script that arrives and draws
  nothing would look identical to one that was never fetched, since both degrade
  silently to the rendition.
