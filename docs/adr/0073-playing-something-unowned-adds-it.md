# Playing something unowned adds it

**Status:** Accepted. **Built.**

**Date:** 2026-08-01

## Context

Playback requires a materialised Part, so anything not in the library had no Play
button at all. A viewer browsing a source's catalogue could Add to library, then
find the item again, then play it — three steps to watch something, two of them
about bookkeeping.

This was scoped as materialise-on-**commitment**: play from a `ContentRef` and
write to the library only past a watch threshold, so bouncing off something after
ninety seconds left no trace. It was deferred on a real collision rather than on
effort: [platform#18](0018-virtual-and-materialized-content.md) admits exactly two
crossings between virtual and materialised, while
[platform#26](0026-playback-state-is-platform-owned.md) keys progress by node — and
pre-commitment progress has no node to key on. Reporting position for something
not yet in the graph has nowhere to put it.

## Decision

**Pressing Play on something unowned materialises it first, and authorises as the
import it is.**

1. **Materialise at play *start*, not at commitment.** This dissolves the
   collision rather than solving it: by the time anything reports a position
   there is a node to key it against, so [platform#26](0026-playback-state-is-platform-owned.md) needs no change and [platform#18](0018-virtual-and-materialized-content.md)'s
   two crossings are untouched.

2. **`playPart` takes the same ref envelope `importContent` takes.** Pressing
   Play on something unowned *is* an import, and giving it a second shape would
   have hidden that.

3. **It authorises as an import.** A viewer without `content.import` is refused
   here exactly as they are refused at the Add button. Play cannot be a way around
   the authority that curates the library ([platform#44](0044-privilege-cannot-escalate.md)),
   so both affordances are drawn under the same condition.

4. **Play comes before Add on the screen.** A viewer wants to watch the thing;
   adding it is what the Platform has to do to let them. Add stays, because
   wanting it in the library without watching it now is a real intent.

5. **A film starts and a series does not.** One playable item is unambiguous.
   A series is many, and **guessing an episode is worse than starting nothing** —
   so an ambiguous work returns cleanly, having been added, and the screen
   re-renders with the episodes that now exist.

## Alternatives

**Materialise on commitment, as originally scoped.** *Rejected on the collision
above.* It is the better outcome and it needs a way to hold progress for a node
that does not exist, which is either a second progress store or a change to what
a node is. Neither is worth what it buys.

**Play virtually, without adding at all.** *Rejected.* Every downstream thing a
playback touches — progress, continue-watching, the resolution cache
([platform#28](0028-resolution-cache-and-capability-classes.md)), subtitle
resolution — is keyed by content in the graph. A virtual playback would need a
parallel identity for all of it.

**Add silently and never tell anyone.** *Rejected*, and this is the one worth
being explicit about: the library gains things people watched ninety seconds of,
and pretending otherwise would make the library quietly untrue. It is stated in
the roadmap and it is the accepted cost.

## Consequences

- **The library gains things people bounced off.** Accepted, and named. The
  library-rule maintenance pass ([platform#60](0060-the-library-is-built-from-rules.md))
  is where a future "remove what nobody finished" would belong, if it is ever
  wanted.
- **An added work with no releases is not an error.** The import succeeded and
  there is nothing to start — a metadata-only source, or nothing installed that
  offers files. It returns the same shape an ambiguous work does, and the screen
  says so through the source picker's empty state
  ([platform#71](0071-a-preference-is-a-default-an-override-is-a-sitting.md)).
- **A viewer who cannot import still cannot play unowned content**, and now sees
  neither button rather than seeing one that fails. That is the same correction
  [platform#44](0044-privilege-cannot-escalate.md) made to the Add button when the first ordinary account pressed it and
  got nothing.
