# Subtitles answer to a person's language preference, in whatever form the track has

**Status:** Accepted. **Built**, with four things named rather than claimed.
The preference key, the settings surface, the per-user audio selection and the
escalation rule are in; delivery as an HLS subtitle rendition is in **for
embedded tracks on the transcoded path**; the form classification, the three
delivery paths and the viewer's setting are in; and every installed subtitles
provider is asked at play time. What is not finished: an **embedded** track on a
**direct-played** release still gets no subtitles, that release carrying no
playlist to hang a rendition off — the module path below is the only one it has;
the styled-script path is
**one step short of reachable**, because the Platform emits the prop through
`ui.Prop` rather than the generated `ui.SubtitleTracks`, the contract carrying it
being committed and untagged; the picture-overlay path **is verified as a
filtergraph and not against a real PGS stream**; whether the client draws a
script correctly is unverified, no browser having run; and `v1.Subtitle` still
cannot say a track is forced, so forced-subtitle behaviour is complete for
embedded tracks and unavailable for module-provided ones.
Consolidates the five records of the subtitle run, whose bodies this replaces.
Their numbers are retired and stay retired, so they are named here by what they
decided rather than cited — a citation would either dangle or, worse, resolve to
some later record that happens to hold the number. **Two of the five were partly
superseded within the run** and the corrections are kept below, under their own
Context headings, rather than in this line: the *delivery* record's offer of
**every** embedded track as a rendition was wrong for picture tracks and lossy
for typeset ones, corrected by the *classification* record that followed it; and
that record's two fates for a typeset track became three under the *styled
script* record after it, which built the client-side renderer the classification
had rejected as blocked and demoted burning to the answer for when it cannot be
used. The **preference** decision itself, the classification's **graphic** path,
and the **module-provided** record were not superseded by anything.
**Date:** 2026-08-10

## Context

This is one decision — *which subtitles a person should be reading* — followed by
a delivery run in which the first answer was corrected twice and then completed by
a fifth record that found the role had never been called.
[platform#71](0071-a-preference-is-a-default-an-override-is-a-sitting.md) is a
neighbour of this run rather than a part of it: it decides what one sitting may
override, where this decides the default.

### Language belongs to a person, not to an install

`internal/transport/playback` picks an audio track by language, and the list it
picks from is a package variable — `PreferredLanguages = {"eng", "en"}` — whose
own comment says what is wrong with it: *"a placeholder for a real user
preference: language belongs to a person, not to an install."* Both callers pass
`nil`, so every viewer on an install gets the same answer.

That is the wrong unit. Mosaic's target is four people sharing one library, and
[platform#59](0059-one-library-many-viewers.md) already established that what a
viewer sees is theirs rather than the install's. Language is the clearest case of
it there is: two people watching the same release want different tracks, and
neither is more correct.

**Subtitles are the half that makes this more than a list.** A person who wants
an English dub usually wants *forced* subtitles with it — the few lines that
translate on-screen text and the occasional foreign phrase, not a transcript of
dialogue they can hear. The same person watching a release with **no** English
dub wants something different: the original audio and the **whole** dialogue in
text. The setting that is right in the first case is close to useless in the
second, and it is the same person with the same preference.

So a subtitle preference expressed on its own is wrong half the time, and which
half depends on something only the Platform knows at play time.

### Why delivery is an HLS rendition

Deciding what a viewer should read left *how it reaches them* open, and until it
was answered the escalation was computed on every play, recorded in telemetry, and
rendered nowhere: `off`, `forced` and `full` were indistinguishable on screen.

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

### Correction 1 — not every embedded track can be a rendition, and one that could not shipped

The rendition design offered **every** embedded track as one. That is wrong for
two of the three kinds of subtitle track a release can carry, and it is wrong in
different ways. Measured, on the origin's own ffmpeg:

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

### Correction 2 — a styled script goes to the client whole, and the block was narrower than it looked

The classification gave a styled subtitle track two possible fates and said the
better one was blocked:

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

### The role that was filled, addressable and unreachable

[module-stremio-addons#1](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0001-completing-the-stremio-source-surface.md)
defined the `subtitles` provider role. Two modules implement it.
`SubtitlesRequest` grew `Season` and `Episode` in SDK `v0.26.0` so a provider
could answer for an episode and not only a film. **Nothing had ever called it.**

The shape of that gap is worth stating precisely, because it is not what
"unbuilt" usually looks like. The registry could resolve a subtitles provider *by
name* — `SubtitlesProvider(id)` — and no code path anywhere knew a name to ask
for. The *plural* enumerator that every other fanned-out role has
(`StreamProviders`, `ArtworkProviders`) did not exist, because nothing had ever
needed one. So the role was fillable, filled, correctly addressable, and
unreachable: not a missing feature, a missing call.

It matters most for exactly the releases Mosaic is built around. A remote source
often has no subtitles of its own, and the embedded path cannot help there —
there is nothing embedded to extract.

## Decision

**Language is a per-user preference, and the subtitle mode it names applies when
the audio preference was met. When it was not, subtitles escalate to full. A
track's codec then decides how it can be delivered — as a rendition, as a script
the client draws, or burned into the picture — and every installed subtitles
provider is asked at play time for what the release does not carry.**

### The preference and the escalation

The preference is one key on the existing per-user mechanism
([platform#59](0059-one-library-many-viewers.md): dotted name, opaque JSON, no
migration for a new one), beside `ui.expert_mode` and `ui.home.rows`:

```
playback.languages = {
  "audio":        ["eng"],      // ordered, most wanted first
  "subtitles":    ["eng"],      // ordered
  "subtitleMode": "forced"      // off | forced | full
}
```

1. **`subtitleMode` describes what you want when you got the language you asked
   for.** It is not a description of the screen; it is a description of a
   satisfied preference.

2. **If the chosen audio track's language is not one the viewer asked for, the
   mode escalates to `full`.** The Platform knows it failed to honour the audio
   preference, and full subtitles are what that failure costs. Escalation only
   ever increases what is shown, and never past `full`.

   Worked through the two cases this was written from:

   | Viewer | Preference | Release | Result |
   |---|---|---|---|
   | Adam | `audio:[eng] subs:[eng] mode:forced` | anime **with** English dub | English audio, **forced** English subtitles |
   | Adam | same | anime **without** English dub | Japanese audio, **full** English subtitles |
   | Maddie | `audio:[spa] subs:[eng] mode:full` | Spanish dub present | Spanish audio, full English subtitles |

   Maddie's escalation is a no-op, which is the point: someone who wants a
   transcript in a second language keeps it whether or not the dub existed.

3. **`off` is honoured and not escalated past.** Someone who has said they want
   no subtitles is not shown subtitles because a dub was missing; they are shown
   the release they can have. Escalation raises `forced` to `full`, and leaves
   `off` alone.

4. **Fallback is by list, then by the release's own opinion, then nothing.**
   Audio already works this way — preferred language in order, then the
   container's default flag, then more channels — and subtitles follow the
   preferred list in order and then stop. **A language nobody asked for is never
   selected**, because a subtitle track in the wrong language is worse than none:
   it occupies the screen and communicates nothing.

### The form of a track decides how it can be delivered

A subtitle track's codec decides how it can be delivered, and burning it into the
picture is the last resort — priced, opt-in where there is a choice, and
automatic only where there is none.

| Form | Codecs | Delivery |
|---|---|---|
| Plain | `subrip`, `mov_text`, `webvtt`, unknown | Rendition. Faithful, free. |
| Typeset | `ass`, `ssa` | The script, sent whole, by default. Rendition beside it, flattened. Burned if the viewer asked for it. |
| Graphic | `hdmv_pgs_subtitle`, `dvd_subtitle`, `dvb_subtitle`, `xsub` | Burned, or nothing. Never offered as a rendition. |

5. **A graphic track is never listed as a rendition.** It cannot become one, and
   listing it produces a menu entry that silently draws nothing — the worst of
   the available failures, because it looks like it worked.

6. **A graphic track is burned when it is the one the preference chose**, and
   only then. A Blu-ray whose only English subtitles are pictures must still be
   able to answer somebody who asked for English, and there is no other way to
   answer them.

7. **Asking for typeset fidelity never burns a plain track.** SubRip has no
   styling an encode could preserve, so there would be nothing to buy with it.

8. **`off` never burns**, and this is the strongest form of the *never select a
   language nobody asked for* rule. Burning is irreversible for the playback, so
   doing it to somebody who asked for no subtitles would put text on their screen
   that they cannot turn off.

9. **Nothing is offered beside a burned track.** It is in the picture already,
   and listing it as a rendition too would draw it twice.

   The two burn paths differ, and the difference is a real cost:

   - **Picture tracks are composited from the stream this run already has open** —
     `[0:N]scale[sub];[0:V]…[main];[main][sub]overlay=eof_action=pass:repeatlast=0[v]`.
     No filename, no escaping, no second read of the source.
   - **Text tracks are drawn by libass**, which can only read a *file*. So the
     source URL is named again inside the filtergraph and opened a second time.
     There is no way around it: the filter cannot be handed a stream that is
     already open.

### Renditions ride on the HLS the origin already serves

10. **The player is told through HLS, not through the SDUI.** The origin already
    serves a playlist to every transcoded release, and every client that can play
    it — hls.js, and Safari natively — already has a subtitle menu, a track
    selector and a WebVTT renderer. Declaring an `EXT-X-MEDIA:TYPE=SUBTITLES`
    rendition reaches all of that with **no contract change and no client
    release**. This is the screen rule's first answer arriving from an unexpected
    direction: the vocabulary does not have to grow, because the thing being
    described is not a component.

11. **The entry point becomes a master only when there is something to declare.**
    `index.m3u8` stays the URL a client is given. It holds the video's segment
    list when the release has no subtitles — byte for byte what it served before
    — and a master naming the video rendition plus one subtitle rendition per
    deliverable track when it has. `CODECS` stays out of the master for the
    reason given for having no master at all: the origin cannot know it before
    the transcode runs, and a wrong one makes a browser refuse a stream it could
    have played.

12. **Every deliverable track is offered; at most one is default.** The rule that
    *a language nobody asked for is never selected* is about selection. The
    rendition list carries every embedded track that can become one so the
    player's own menu works, and `DEFAULT=YES` marks the one the preference and
    its escalation chose, or none. That is also the only part of a track picker
    (M3 item 6) this delivers, and it delivers it by not building one.

13. **A subtitle rendition has its own grid: sixty seconds, ten times the
    video's.** HLS requires renditions to describe the same running time, not to
    divide it the same way. A subtitle segment costs one ffmpeg run and one range
    read over the container, so cutting on the video's six-second grid would pay
    that ten times as often for the same bytes. A minute is also more than any
    player reads ahead.

14. **The window is bounded by `-to`, not `-t`.** `-t` bounds the output's
    *duration*, and `-copyts` has already rebased the output onto the source's
    clock — so `-ss 60 … -t 60` stops the instant it starts. Measured against a
    file with a cue every ten seconds: window 0 yielded six cues, window 1
    yielded none. As a shipped bug that is every subtitle in a film vanishing one
    minute in, and it is recorded here because it was the second time this
    repository had been caught by an ffmpeg flag that reads correctly and
    measures wrong.

15. **Nothing is spooled.** A window of dialogue is a few hundred bytes and goes
    straight to the response. The segment spool exists because a video segment
    must be complete before it is served; a WebVTT document has no such
    constraint, and inventing a spool for it would add a reaper, a lifetime and a
    disk budget to something smaller than the HTTP headers around it.

### A styled script goes to the client whole

16. **`Player` gains `subtitleTracks`** — a list of `{src, format, language,
    label, default}`, specced in `contracts` with `SubtitleTracks` sugar
    generated into Go and TypeScript. Named `SubtitleTracks` rather than
    `Subtitles` deliberately: `Subtitle` is an existing prop on a different tier,
    and a builder one letter from another is how `ui.Subtitle` came to be set on
    a `Stack` that has no subtitle and drew nothing for the life of a screen.

17. **The scripts ride *beside* the HLS renditions, never instead of them.** This
    is what makes the whole thing safe to ship before any client implements it,
    and it is the design rather than a courtesy: a client that cannot draw a
    script ignores the prop and uses the flattened rendition the playlist already
    declares. There is no negotiation, no capability flag and nothing for the
    Platform to get wrong about what a client can do.

18. **The viewer's choice for a typeset track is three-valued** — `plain`,
    `client`, `burn` — replacing the earlier boolean `typeset` setting, whose
    `true` meant `burn`. **`client` is the default, because it dominates**: it
    preserves everything, costs no encode, and degrades on its own. A document
    written before this field says `typeset: true` and still resolves to `burn`,
    so nobody's playback changes under them.

19. **The client echoes the server's choice and does not rank.** It draws the
    track marked `default`, else the first offered. Which subtitles a person gets
    was decided by the Platform against the release; re-deciding it in a renderer
    would give two clients two different answers from one preference.

20. **Every client-side failure is silent.** A browser too old for the WASM, a
    blocked asset, a script that will not parse — each leaves a player that still
    has subtitles, so none is worth reporting as a playback error.

### What the release does not carry, a module may

21. **Every installed subtitles provider is asked at play, not stored at
    import.** A subtitle URL is perishable in the same way a debrid link is, and
    resolving them into the graph at import buys entries that have been decaying
    since before anyone wanted them — the mistake
    [platform#28](0028-resolution-cache-and-capability-classes.md) already names
    for streams.

22. **Fanned out over every provider, handed shared identities.** The same shape
    stream enrichment uses and for
    [platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)'s
    reason: a subtitles provider is asked about content it did not source, so it
    gets a neutral external id rather than a native one it could not have.

23. **Best-effort throughout.** Every failure logs and continues. A subtitle
    source that is down, unconfigured or simply does not know the title costs the
    extra tracks and never the playback.

24. **The origin fetches; the URL never reaches a client.** A module resolves and
    the Platform serves
    ([platform#25](0025-playback-consumer-and-media-origin.md)), and the reason is
    concrete rather than architectural hygiene: the URL may carry a credential,
    and pointing a browser at it also hands a third party the viewer's address.

25. **ffmpeg does the fetching as well as the conversion.** It already speaks
    every scheme a module might return, already carries the reconnect behaviour
    the rest of the origin needs, and a file that is already WebVTT costs a
    passthrough rather than a second code path.

26. **None of them is ever default.** The release's own tracks are what a viewer's
    preference was resolved against; a file from elsewhere turning itself on would
    override a decision nobody asked it to make. The viewer picks one from the
    player's menu.

## Alternatives considered

**Leave language an install-wide setting.** *Rejected*, and it is what existed. It
answers for one person on a machine built for four, and it silently gives the
other three someone else's language.

**Two independent settings — an audio preference and a subtitle preference — with
no coupling between them.** *Rejected*, and this is the tempting one because it is
simpler to describe. It is wrong in the case people actually hit: a viewer who
sets "forced" gets three lines of signage over two hours of a language they do not
speak, and has to know to go and change a setting to fix a release rather than a
preference. The coupling is the feature.

**Always full subtitles when any are wanted.** *Rejected.* It burns the dub case
for the many people who do not want a transcript of dialogue they can hear, and it
makes `forced` unexpressible.

**Per-title overrides.** *Out of scope here, not rejected.* Choosing a specific
track for one playback is the track picker (M3 item 6) and belongs with the source
picker's surface. A preference decides the default; an override decides one
sitting — which is
[platform#71](0071-a-preference-is-a-default-an-override-is-a-sitting.md)'s
subject, not this record's.

**A sidecar `<track>` on the player, via a `Player` subtitles prop.** *Rejected as
the first delivery, not wrong.* It is the only design that also covers
direct-played releases, and it is the natural follow-up. It was not chosen first
because it costs a contract change, a generated-code regeneration and a client
release to deliver what HLS already delivers for free on the path that needs it
most — and because the `contracts` publish train was not movable. A narrower form
of it was later taken: a prop naming *scripts* rather than a WebVTT sidecar, with
the renditions still carrying the fallback.

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

**Burn subtitles into the video, as the delivery mechanism.** *Rejected.* It
forces a video encode on a release that may not otherwise need one, which is the
single most expensive thing the Platform can do, and it makes the choice
unchangeable for the rest of the playback. That rejection was later **narrowed
rather than upheld**: burning is not a delivery mechanism, but it is the only
answer for a graphic track and the only faithful one for a typeset track a client
cannot draw, so it survives as the last resort under both of the rules below.

**Burn everything, always.** *Rejected.* It is simpler and it is what a naive
reading of "subtitles that appear over stuff" asks for. It also forces the most
expensive operation the Platform has onto every release with a subtitle track,
including the ones where a free rendition would have been identical.

**Never burn; flatten what can be flattened and drop the rest.** *Rejected.* It
makes a Blu-ray's only English subtitles unreachable, and it makes typeset signs
permanently unavailable rather than merely expensive.

**Send the ASS to the client and render it there with libass-wasm.** *Rejected as
blocked, then taken.* It preserves everything, costs no encode, and JASSUB does it
in a browser today. It was held because it needs the client handed an ASS URL,
which means a prop on the `Player` component — a native-vocabulary growth in
`contracts` with a `@mosaic-media/sdui-react` bump — and the publish train has
been returning 403 on tag pushes since before this work began. Re-examined, the
403 stops the tag and not the spec, the client or a producer willing to emit an
unbuilt prop by name, so this is what the Platform now does.

**Convert ASS to WebVTT with positioning cues.** *Rejected twice, though
tempting.* WebVTT does have `line`, `position` and `align`, so a fraction of ASS
would survive a careful mapping — but not `\pos` at arbitrary coordinates, not
colours, not fonts, not the layered effects typesetting actually uses. It would
produce subtitles that are wrong in a new and harder-to-explain way, and it would
still need the burn for everything it could not express.

**Wait for the tag before shipping the script path.** *Rejected.* It is the tidy
answer and it makes the work wait on something with no known resolution and no
owner in this session. The spec, the generated builders and the client are all
landable now, and the producer's one line is a documented swap rather than a
design question.

**Declare ASS support on `ClientProfile` and let the Platform decide.** *Rejected*,
and it was the first design. It needs a protobuf field — blocked on the same tag —
and it buys nothing: offering both forms lets the client decide by whether it can,
which is more accurate than any declaration and cannot go stale.

**Ship the script by extending the HLS master with a non-WebVTT rendition.**
*Rejected.* HLS subtitle renditions are WebVTT; a player handed anything else
there fails rather than ignoring it, which is the opposite of the degradation this
design rests on.

**Resolve module subtitles at import, beside stream enrichment.** *Rejected.* It
is where the code would naturally have gone, and it caches perishable URLs at the
moment they have the longest time to go stale before anyone plays.

**Hand the client the module's URL and let it fetch.** *Rejected.* Fewer moving
parts, and it leaks a credential and the viewer's address to a third party. It
also breaks the moment a provider needs a header, which the origin can supply and
a `<track>` element cannot.

**Convert with a Go SubRip parser rather than ffmpeg.** *Rejected.* It is a small
library and it would be a second fetching path, a second scheme table and a second
set of reconnect behaviour, to avoid a process for a file measured in kilobytes.

## Consequences

- **`SummaryAudioCodec` becomes wrong for someone.** It picks a track using the
  install-wide list and stores that track's codec as a column on the Part, where
  it feeds candidate ranking
  ([platform#27](0027-stream-selection-against-a-client-profile.md)). Once
  language is per-user, two viewers want different answers from one column, and it
  would be wrong silently rather than loudly. The full track list is already
  stored on the Part as a document, so per-user selection reads that and the
  column stays a coarse ranking hint — which is all ranking needs, since it asks
  "will this need an audio encode at all".
- **The resolution cache is unaffected**, which is worth stating because it looks
  like it should be.
  [platform#28](0028-resolution-cache-and-capability-classes.md) caches the
  resolved upstream address keyed by part and capability class; the per-stream
  decision is made after, from the probe. Language changes the plan and not the
  address, so no key needs to grow.
- **The audio half was deliverable on its own and did something.** The parameter
  existed and was passed `nil`; filling it gets a viewer the dub they asked for.
  The subtitle half needed delivery, which is M3 item 5 and the larger piece — so
  this landed in two parts, and the part that makes the escalation rule visible
  was the second one.
- **A preference nobody can set is not a preference.** It ships with a surface to
  set it in the same change, or it is an
  [unreachable capability](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md) row rather than a
  feature.
- **Direct-played releases get no *embedded* subtitles.** A relayed stream is the
  upstream's own bytes and the origin adds no playlist to hang a rendition off.
  That gap is real — a release that needs no transcoding is the common and the
  wanted case — and it is closed only from the module side: a file from elsewhere
  needs no playlist, so it is served on the relayed path too, which is why the
  subtitle resource is reachable under a direct-play ticket, the only sub-resource
  that is. An embedded track on a direct-played release still needs the sidecar
  design.
- **The extra read is the cost, and it is proportional rather than up front.**
  Playing with subtitles on reads the container roughly twice — once for the video
  segments and once for the subtitle windows — because the two are separate
  processes and there is no way to reach the text without the pictures.
- **Module-provided subtitles are now reached, and they are the cheap path.** A
  `subtitles` capability returns a URL to a file, which needs no extraction at all
  and is far cheaper than reading a container twice — it was unaffected and
  unreached under the rendition design, neither closed nor blocked by it, and it
  is what the missing call finally reached.
- **Deduped by URL.** A title carrying both an IMDb and a TMDB id would otherwise
  list every file twice, once per identity the provider was asked under. The first
  identity that answers ends the questioning for that provider, for the same
  reason.
- **The module is named in each label.** Two sources answering for one title is
  the ordinary case, and a menu with "English" twice is one a viewer cannot choose
  from.
- **`v1.Subtitle` cannot say a track is forced.** The probe's `SubtitleTrack`
  carries `Forced`, so an embedded track can be classified; a module returning
  subtitles reports only language, URL and id. Forced-subtitle behaviour is
  therefore complete for embedded tracks and unavailable for module-provided ones
  until the SDK gains the field — an additive bump whenever the publish train
  moves.
- **Subtitles can turn a direct-play into a transcode.** A release that would have
  been relayed untouched is re-encoded if the viewer asked for a burn and the
  track is ASS. This is the one place where a *preference* moves a release across
  the cheap/expensive line, and it is why the setting says what it costs.
  `subtitle_burned` is on the play telemetry for the same reason: from the outside
  this presents only as a release that suddenly plays badly.
- **A burned track cannot be switched off.** By the time it reaches the client it
  is part of the picture, so the player's subtitle menu is empty for that
  playback. A rendition is switchable and a burn is not, and that asymmetry was
  the strongest argument for the client-side path — which is why that path is now
  the default and **burning is genuinely last**, reached only by a graphic track,
  which has no other delivery, or by a viewer who chose it. That is the right
  shape for a machine where a re-encode may be the difference between a release
  that plays and one that does not.
- **The libass burn path reads the source twice.** Once for the frames and once
  for the filter's own demuxer. Confirmed in the access log of a local server
  while measuring the escaping.
- **Extracting a script costs one read of the container, and it cannot be
  windowed.** An ASS script is one document — a header, a style table, then the
  events — and libass needs all of it before it draws a line. So the origin
  extracts with no `-ss` and no `-copyts`, and the script arrives while playback
  proceeds. That is the same read burning would have done, minus the encode.
- **Filtergraph values are escaped twice, and getting it wrong fails silently.** A
  filtergraph is unescaped once to split filters from options and again per option
  value, so `https://host` must reach ffmpeg as `https\\://host`. Single-escaped,
  ffmpeg read `//host` as an unrelated option, reported "unable to parse option
  value as image size", **then encoded the video with no subtitles on it and
  exited successfully**. That is the third measured ffmpeg flag in this area that
  reads correctly and behaves wrongly, after `-read_intervals`
  ([platform#82](0082-the-origin-relays-or-serves-a-nominal-segment-grid.md)) and
  the `-t` window above.
- **`-copyts` is load-bearing for burn-in, not only for seeking.** The `subtitles`
  filter reads the source independently and emits cues on the source's own clock.
  Measured: with `-ss 12 -copyts` a cue authored at 14 s burned at 14.0–17.8 s,
  matching the unseeked control exactly; **without `-copyts` it did not appear at
  all**, because the frames had been rebased to zero and the cues had not.
- **The picture path is verified as a graph and not against real PGS.** ffmpeg
  cannot encode text subtitles into a bitmap format, so no fixture could be built
  here; the filtergraph was exercised with a stand-in overlay stream and accepted.
  What is unverified is PGS decoding and palette handling, which a real Blu-ray
  release will settle.
- **`sdui-react` gains a dependency and both apps gain a build setting.** jassub
  is dynamically imported, so a playback with no styled track pays nothing for it.
  Vite's worker format moves to ES in the Shell and the storybook, because that
  worker code-splits and Rollup refuses to emit a split build as IIFE.
- **No asset paths are named from Mosaic's side.** jassub locates its own worker,
  its two WASM builds and its fallback font relative to its own module, which is
  the one form a bundler rewrites into a built asset address. Two attempts at
  naming them failed first — a `?url` import Rollup cannot resolve out of a
  pre-built dependency, then hardcoded paths that had moved between versions — and
  the build now emits all five assets.
- **The Platform emits the prop with `ui.Prop("subtitleTracks", …)`, not the
  generated builder.** The builder exists — in the spec, and in `contracts`'
  `main` — and not in the version the Platform compiles against, because that
  requires a tag. This is the narrow case the "author with the generated builders"
  rule allows ("a type the spec does not cover yet — and then add it to the
  spec"), and the reason the rule exists does not apply: the prop is not a string
  nobody renders, since `@mosaic-media/sdui-react` draws it. **It is still one line
  owed**, and it is on the roadmap rather than left to be noticed.
- **Whether a script draws correctly is unverified.** The build emits the assets
  and the extraction preserves every tag (`\pos`, `\c`, `\fs`, the style table,
  measured), but no browser ran. A script that arrives and draws nothing would
  look identical to one that was never fetched, since both degrade silently to the
  rendition.
- **One claim about the rendition path is unverified and it is a client-side
  one.** The origin's two clocks were measured to agree: with `-copyts`, video
  segment 10 of a six-second grid begins at PTS 55.023 — one GOP early, which
  [platform#82](0082-the-origin-relays-or-serves-a-nominal-segment-grid.md)
  allows — and the cue for 60 s reads 60.023, the same clock with the same
  container start offset. Whether hls.js maps a raw WebVTT segment onto an fMP4
  timeline without an `X-TIMESTAMP-MAP` header cannot be measured without a
  browser. No header is injected, because a mapping written blind is worse than
  none. If subtitles land at a constant offset, that is the cause and it is one
  constant to fix.
