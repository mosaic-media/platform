# Ordering is renumbered when it runs out of room, and confidence does not decay

**Status:** Accepted. **Not built.** Settles the two things
[platform#9](0009-object-graph.md) recorded as unsettled: the fractional scheme
`natural_order` uses at large scale, and whether relation confidence ages.

## Context

`natural_order` is a float so an item can be inserted between two others without
renumbering the ones around it. platform#9 says the exact scheme at large scale is
not settled, and the reason it matters is specific: repeated midpoint insertion
between the *same pair* exhausts double precision after roughly fifty goes, and
the failure is two rows comparing equal — an ordering that silently stops being
one.

`relation.confidence` is a double between 0 and 1 with a `CHECK` constraint, and
the migration's own comment records that it is written once at creation and that
nothing ages or rechecks it. Nothing reads it either.

## Decision

**The float stays, and a container is renumbered when its gaps get too small.**
Insertion remains a single cheap write in every ordinary case; the pathological
case is repaired locally, over one container's rows, and rarely. No column type
change and no data migration.

**The repair happens in the transaction that would have exhausted the gap**, not
afterwards. This is the part that is easy to get wrong: a nightly pass that widens
gaps is genuinely useful, but it cannot be the correctness mechanism, because
fifty insertions between one pair can happen in a single session — long before any
night arrives. Between exhaustion and repair the ordering is already wrong, and a
reader in that window sees two rows tied with nothing to break it.

**A scheduled pass is adopted as well, as hygiene rather than as the fix.** It
widens gaps across containers so the in-transaction renumber fires rarely, and the
Platform already has the machinery: a job runner with a scheduler, and a daily
maintenance job alongside which this one sits. The distinction is worth keeping
explicit — the transaction guarantees correctness, the schedule keeps the
guarantee cheap.

**Relation confidence does not decay. A re-import is the only thing that changes
it.** The score moves when new information arrives and never otherwise. No timer,
no policy to tune, and no background job — the trigger is the same event that
produced the edge.

## Alternatives considered

**Lexicographic fractional indexing on a string column.** *Rejected:* it removes
the failure mode rather than handling it, at the cost of a column type change, a
data migration, and larger keys in every ordering comparison and index — for a
problem no library has hit.

**Integer positions with gaps.** *Rejected:* the same renumber with less headroom,
and it re-introduces the renumbering platform#9 chose a float to avoid.

**A nightly pass as the whole answer.** *Rejected on its merits, and adopted
alongside:* it is the right hygiene and the wrong guarantee, for the reason above.

**Time decay on confidence.** *Rejected:* a number that moves with no new
information is a fabricated signal. It looks like evidence and is arithmetic, and
the first consumer to read it would be reading the clock.

**Periodic reverification.** *Rejected:* re-asking every source about every edge,
on a schedule, for a score nothing currently reads.

## Consequences

**An edge from a source nobody re-imports keeps its original score forever.** That
costs nothing today because nothing reads it, and the first consumer that does is
when to revisit this — at which point "how old is this assertion" is a question
`created_at` can already answer without the score pretending to.

**The renumber has to be transactional with the insert that triggers it**, which
makes one ordinary insert occasionally an expensive write. It is bounded by a
container's size, which is a season or a collection rather than a library.

**Two jobs with different purposes and one appearance.** A reader finding the
nightly pass could reasonably conclude it is what keeps ordering correct. It is
not, and the code should say so where it lives, because removing it as "an
optimisation nobody needs" would be safe and removing the transactional path would
not.
