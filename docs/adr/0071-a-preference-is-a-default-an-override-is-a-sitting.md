# A preference is a default, an override is one sitting

**Status:** Accepted. **Built.** The candidate list, the picker screen, the
no-candidate state and the per-play audio and subtitle overrides are in. The
picker screen has no track controls on it yet — see Consequences.

**Date:** 2026-08-01

## Context

Two M3 items turned out to be the same decision seen from opposite ends, and
neither could be finished without answering it.

**Item 2: selection is invisible.** [platform#27](0027-stream-selection-against-a-client-profile.md)
ranks candidate releases against the calling client and plays the winner. It
reports what it chose *out of how many* — a count, added precisely because
"nothing changed" looks identical whether ranking picked badly or the item only
ever had one candidate. But a count is not something a person can act on, and
there was no way to override the choice. Worse, an item with **no** playable
candidate presented as a *failure*: an ordinary situation — a metadata-only
import, a source that has stopped offering something — rendered as though
playback were broken, which sent people looking for a bug that was not there.

**Item 6: the preference had no escape hatch.**
[platform#67](0067-language-is-a-persons-preference.md) made language a per-user
preference and its own Alternatives section deferred the other half:

> **Per-title overrides.** *Out of scope, not rejected.* Choosing a specific
> track for one playback is the track picker (M3 item 6) and belongs with the
> source picker's surface. A preference decides the default; an override decides
> one sitting.

So both items need the same thing said clearly: **what a person always wants and
what they want right now are different facts, and the second must not overwrite
the first.**

## Decision

**The Platform shows its working and lets a viewer choose within it, for one
playback, without changing what they always want.**

1. **The candidate set is a list, not a length.** `PlaybackSources` returns the
   ranked releases behind an item, each carrying what would have to happen to
   play it — a video re-encode, a tone-map, an audio re-encode, or nothing at
   all. **The phrasing is about the client, not the file:** a release is not
   "bad", it is undecodable by the thing asking, and the same release on a
   television may be the best answer available.

2. **It ranks and does not resolve.** Resolving a candidate is a call to an
   aggregator costing hundreds of milliseconds
   ([platform#28](0028-resolution-cache-and-capability-classes.md)); resolving
   twenty to draw a list would spend a play's entire latency budget on a screen
   somebody may only be glancing at. A picker names releases. Picking one is an
   ordinary play of that Part, and that is where its address is fetched — which
   is also why this needed no new action kind.

3. **Nothing playable is an answer, not an error.** The empty case gets the same
   screen, saying plainly that an item can be in the library with no file behind
   it, and pointing at Extensions. An error was never true and read as a defect.

4. **An override names a stream for this playback only.** `playPart` accepts an
   audio and a subtitle stream index. Neither is written back: **a preference
   decides the default and an override decides one sitting.** Somebody sampling
   the Japanese audio on one episode has not changed what they want on the next.

5. **An override re-decides; it does not re-label.** Whether audio is copied or
   encoded is a property of the *chosen track's* codec, and whether the playback
   direct-plays is a property of that. Switching from the AAC track to the DTS
   one on a browser turns a direct play into a transcode, and the plan says so.

6. **An index naming no track leaves the plan alone.** The menu came from a probe
   and a release can be re-probed under it, so ignoring a stale choice is better
   than losing the audio entirely.

## Alternatives

**Keep the count and add nothing.** *Rejected*, and it is what existed. The count
was already an admission that the outcome was unreadable; adding a second number
would not have made it actionable.

**Resolve every candidate so the picker can show live availability.** *Rejected.*
It is the version a viewer would prefer and it costs a play's whole latency
budget, times the number of candidates, on a screen that is often just glanced
at. A release that turns out dead when picked is handled by invalidate-on-read
([platform#28](0028-resolution-cache-and-capability-classes.md)), which is where that failure belongs.

**Make the override sticky — remember the last track per item.** *Rejected, and
this is the tempting one*, because it feels helpful. It quietly turns every
experiment into a setting: a viewer who samples a commentary track has now
committed to it for that title, and there is nowhere to see or undo that. If
"always do this" is wanted, it is a preference, and there is a screen for
preferences.

**Let the client choose the track from the tracks it can see.** *Rejected.* The
client can see the HLS renditions and would then be re-deciding a preference the
Platform already resolved against the release — two clients, one preference, two
different answers. The client echoes; the server decides
([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)).

## Consequences

- **`SourcePicker` has been in the contract the whole time and nothing emitted
  one.** The definition existed, every client could render it, and no server
  surface ever sent one — so selection's answer was visible only as a number in a
  log. This is a definition finally reaching a screen rather than a vocabulary
  growth, which is the screen rule working rather than a coincidence.
- **The picker screen carries releases and not yet tracks.** The audio and
  subtitle overrides are honoured on the play path and reachable by any client
  that sends them; the screen that would offer them as controls is not built.
  Embedded subtitle tracks are separately switchable in the player's own menu
  ([platform#68](0068-subtitles-are-a-rendition.md)), so the visible gap is audio. That is an
  [unreachable capability](../unreachable-capability.md) row until the surface
  lands.
- **A stable tiebreak was needed and is not cosmetic.** Two candidates that rank
  equally must come back in the same order every render, or the list moves under
  a viewer reaching for the third row. The source's own order breaks ties.
- **An undeclared client is told nothing rather than something optimistic.** With
  no profile there is no basis for "plays directly", so the quality summary
  states the release's own facts and the playability line is empty.
