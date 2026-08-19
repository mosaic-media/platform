# Precedence is ordered by the operator, and dedup is exact or not attempted

**Status:** Accepted. **Not built**, except as the special case it replaces.
Closes the seam [platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)
named from two directions and
[sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)
named as precedence. Gives the composition root's fallback tier a record, and
retires it as a mechanism.

## Context

This is not hypothetical and has already forced a workaround. The composition
root registers Cinemeta as a **fallback**, reached by the browse roles only when
no ordinary provider answered, with the reason written beside it: ordering merely
by id is not enough, because with both providers answering, a keyed deployment
draws Cinemeta's catalog rows beside TMDB's and shows the same films twice. That
is a precedence mechanism, invented at the composition root, with no record
behind it and no way for anyone but a Mosaic developer to change it.

The other direction is platform#46's: a stream provider is now asked about content
it did not source, nothing stops two providers returning the same release, and
cross-provider Part dedup was left open.

## Decision

**Precedence is an ordered list, held by the operator, per role class.**

Per role class rather than one global order, because a module strong at metadata
can be weak at catalog and a single order forces a compromise nobody can express.
The existing fallback tier is already role-scoped in behaviour — it applies to the
browse roles specifically — so this generalises what is there rather than adding a
dimension.

Operator-ordered rather than module-declared reuses something already settled:
[platform#89](0089-annotations-are-facts-and-documents-ordered-by-the-operator.md)
put annotation precedence in the operator's hands for the same reason, and an
addon list inside a module is already ordered this way. A module that ranked
itself would rank itself first, and nothing could check the claim.

**The fallback tier stops being a mechanism and becomes a default order.**
Cinemeta last is a shipped default, not a special register. That keeps
[module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md)'s guarantee — a fresh install with nothing configured still has
a metadata floor — while making the arrangement something an operator can see and
change rather than a flag in a composition root.

**Dedup is exact or it is not attempted.**

Two levels, and they already differ. A **Work** is deduplicated today by external
identity: content bound under the `imdb` scheme makes a title added by one
IMDb-keyed source the same Work another one added, which is why changing that
scheme doubles a library. That mechanism stands and is what "dedup on external
identity" means at this level.

A **Part** has no such identity in general, and this is where the record declines
to invent one. Where a release carries a content hash — an infohash is exactly
this — two Parts with the same hash are the same bytes and are merged. Where there
is no hash, **nothing is merged.** A tuple of container, codecs, resolution and
size looks like an identity and is not: two genuinely different releases can agree
on all of it, and a false merge deletes a playable release in favour of one that
may not play. Near-duplicates stay, and selection ranks them, which is what
selection is for.

## Alternatives considered

**A module declares a quality or confidence score.** *Rejected:* every module
ranks itself first, and nothing can check it. It is the same unverifiable shape as
a declared media type, with a consequence a user sees on every screen.

**One global precedence order.** *Rejected:* it cannot express "this source for
artwork, that one for catalogs", which is the ordinary case as soon as more than
two modules are installed.

**Fuzzy Part dedup on a descriptive tuple.** *Rejected:* the failure is silent and
costs a working release. An unmerged duplicate is visible and annoying; a wrongly
merged pair is invisible and unplayable.

**Keep the fallback tier and add ordering beside it.** *Rejected:* two mechanisms
answering the same question, where the one nobody can see wins.

## Consequences

**A fresh install needs a shipped default order**, so "operator-ordered" still
means somebody picks the starting arrangement. That choice is a product opinion
rather than a mechanism, and it belongs beside the module list rather than in a
composition root where only a developer can reach it.

**Installing a module changes what a screen shows, in a position the operator did
not choose.** A newly installed module has to land somewhere in each order it
participates in, and last is the only defensible default — it cannot displace what
a user already relies on.

**Duplicate Parts remain visible** in the case with no hash, which is most direct
HTTP links. That is the accepted cost of refusing a fuzzy merge, and it is the
kind of thing that reads as a defect in a screenshot; it is worth saying in the
interface rather than leaving a user to conclude the library is broken.
