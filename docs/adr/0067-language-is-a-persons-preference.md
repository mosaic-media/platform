# Language is a person's preference, and subtitles answer to whether it was met

**Status:** Accepted. **Built for embedded subtitle tracks on the transcoded
path.** The preference key, the settings surface, the per-user audio selection,
the escalation rule and its delivery as an HLS subtitle rendition
([platform#68](0068-subtitles-are-a-rendition.md)) are in. Two gaps remain, both
named there: a direct-played release carries no playlist and so gets no
subtitles, and a module-provided subtitle still cannot say it is forced.
**Date:** 2026-07-31

## Context

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

## Decision

**Language is a per-user preference, and the subtitle mode it names applies when
the audio preference was met. When it was not, subtitles escalate to full.**

The preference is one key on the existing per-user mechanism —
[dotted name, opaque JSON](0103-one-library-many-viewers.md), no migration for a
new one — beside `ui.expert_mode` and `ui.home.rows`:

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

## Alternatives

**Leave it an install-wide setting.** *Rejected*, and it is what exists. It
answers for one person on a machine built for four, and it silently gives the
other three someone else's language.

**Two independent settings — an audio preference and a subtitle preference —
with no coupling between them.** *Rejected*, and this is the tempting one because
it is simpler to describe. It is wrong in the case people actually hit: a viewer
who sets "forced" gets three lines of signage over two hours of a language they
do not speak, and has to know to go and change a setting to fix a release rather
than a preference. The coupling is the feature.

**Always full subtitles when any are wanted.** *Rejected.* It burns the dub case
for the many people who do not want a transcript of dialogue they can hear, and
it makes `forced` unexpressible.

**Per-title overrides.** *Out of scope, not rejected.* Choosing a specific track
for one playback is the track picker (M3 item 6) and belongs with the source
picker's surface. A preference decides the default; an override decides one
sitting. This record is about the default.

## Consequences

- **`SummaryAudioCodec` becomes wrong for someone.** It picks a track using the
  install-wide list and stores that track's codec as a column on the Part, where
  it feeds candidate ranking ([platform#27](0027-stream-selection-against-a-client-profile.md)).
  Once language is per-user, two viewers want different answers from one column,
  and it would be wrong silently rather than loudly. The full track list is
  already stored on the Part as a document, so per-user selection reads that and
  the column stays a coarse ranking hint — which is all ranking needs, since it
  asks "will this need an audio encode at all".
- **The resolution cache is unaffected**, which is worth stating because it looks
  like it should be. [platform#28](0028-resolution-cache-and-capability-classes.md)
  caches the resolved upstream address keyed by part and capability class; the
  per-stream decision is made after, from the probe. Language changes the plan
  and not the address, so no key needs to grow.
- **The audio half is deliverable on its own and does something.** The parameter
  exists and is passed `nil`; filling it gets a viewer the dub they asked for.
  The subtitle half needs delivery, which is M3 item 5 and the larger piece — so
  this lands in two parts, and the part that makes the escalation rule visible is
  the second one.
- **`v1.Subtitle` cannot say a track is forced.** The probe's `SubtitleTrack`
  carries `Forced`, so an embedded track can be classified; a module returning
  subtitles reports only language, URL and id. Forced-subtitle behaviour is
  therefore complete for embedded tracks and unavailable for module-provided ones
  until the SDK gains the field — an additive bump on the same terms as the
  others.
- **A preference nobody can set is not a preference.** It ships with a surface to
  set it in the same change, or it is an
  [unreachable capability](../unreachable-capability.md) row rather than a
  feature.
