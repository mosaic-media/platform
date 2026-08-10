# The library is built from rules, and a job maintains it

**Status:** Accepted and **built in part** in roadmap M2a, on M0.1's runner,
scheduler and system principal. Built: the rule store, the settings surface, the
scheduled pass, and the run's account on each rule. Not built: the **query
kind's client path** — a saved provider search is stored, evaluated and run by
the same code with no surface to create one from — and **tree refresh**, so a
series that gains a season is refreshed and does not grow the new season,
because every module dedups before writing and returns `AlreadyKnown`. Builds on
[platform#18](0018-virtual-and-materialized-content.md)'s curated act and
[platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)'s
enrichment pass. Produces the one shared library
[platform#59](0059-one-library-many-viewers.md) describes.
**Date:** 2026-07-26

## Context

[platform#18](0018-virtual-and-materialized-content.md) split content into a
**virtual** plane — what a provider returns on read, never persisted — and a
**materialised** plane, the object graph, written only by an explicit curated
act. That structure is right and it is what stops a browse from flooding the
store.

The curated acts that actually exist are both single, manual and forgetful: a
user presses *Add* on a search result, or an administrator opens a module
catalog and publishes from it. So the library is whatever individuals happened
to press Add on, and **nothing anywhere states what the library should
contain**. Because nothing states it, nothing can notice when it stops being
true:

- a series gains a season, or a season gains an episode;
- a catalog gains a title that matches exactly what somebody curated last month;
- a release is superseded by a better one, or the Parts an item has all rot;
- artwork improves because an artwork provider was installed after the import.

Every one of those is invisible until a human re-imports the item by hand. The
enrichment pass added by
[platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)
already knows how to fill in what is missing, idempotently — it runs on import
and never again.

Two mechanisms exist and have never been connected to each other. A **catalog**
is a module's own collection, browsable and paged. `SearchContentQuery` is a
**library query** with media type, kind, title substring and attribute
containment. Neither is durable, and neither describes an intent.

## Decision

**A library rule is a durable, administrator-owned statement of what the library
should contain. The library is the union of its rules' results and everything
added by hand. A scheduled job evaluates the rules and reconciles.**

**A rule is one of two kinds, and each reuses a mechanism that already works.**

- A **collection rule** names a module catalog the way the catalog screen
  addresses one — module id, catalog id, native type — optionally bounded to the
  first N items.
- A **query rule** is a saved provider search.

There is deliberately no third kind. A rule over the library's *own* contents is
a **view**, not a source, and belongs with faceting on the Library screen; a rule
that reads what a previous rule wrote is a loop with no fixed point.

**A rule is Platform-owned state.** It references a module the way a screen does,
and it survives that module being uninstalled — degraded and visibly so, never
deleted, because an extension is removable at runtime
([platform#51](0051-extension-installation-is-user-initiated-and-persistent.md)) and
a rule outliving it is the honest outcome.

**Rules add. They do not remove.** Reconciliation materialises what is missing
and refreshes what is stale. A title that leaves a catalog stays in the library.
A source's churn is not a household's decision, and silently deleting something
somebody watched half of is the worst thing this feature could do. Removal stays
a manual act.

**The job runs as the system principal**, not as the administrator who wrote the
rule. A maintenance write must not fail because that person's authority changed
or their account was suspended, and the resulting nodes should be attributable to
the install rather than to somebody who pressed nothing. This is the same
principal four other callers want, and it is why this record cannot start early.

**Reconciliation is idempotent, bounded and best-effort per item.** The
enrichment pass already fills only items with no Parts, so a re-run attaches no
second copy; one item failing logs and continues, because a run that produced
ninety-nine correct nodes has succeeded. A run is bounded and its schedule is
configuration, because rules turn a household's upstream load from bursty and
human-triggered into continuous — which is a new way to exhaust a rate limit.

**Every run records what it did** — created, refreshed, skipped, failed — where a
person can read it. A library that maintains itself is a library whose contents
nobody chose item by item, and the run log is the only account of why something
is there.

## Alternatives considered

**Keep curation manual only (the status quo).** *Rejected.* It is what makes the
library simultaneously unmaintained and unmaintainable at household scale, and it
is why nothing in Mosaic can answer "why is this here".

**Mirror a source exactly, removing what the source dropped.** *Rejected.* It
makes a third party's churn into deletions from a household's library, and the
failure is silent and unrecoverable. A user can see something they did not want;
they cannot see something that is gone.

**Let a module declare what to import.** *Rejected.* It inverts
[platform#18](0018-virtual-and-materialized-content.md)'s curated act — the module
would decide what the install owns — and it needs module-declared cron, which is
deferred for its own reasons.

**Materialise everything a source offers.** *Rejected.*
[platform#18](0018-virtual-and-materialized-content.md)'s structural answer to "do
not overwhelm the store" is that uncurated content never reaches one, and a
Stremio-class aggregator exposes far more than anyone curated.

**A rule as a saved search the user re-runs by hand.** *Rejected*, though it is
the cheap version. It is a bookmark, not a statement of intent, and it maintains
nothing — the whole value here is that something runs when nobody is watching.

## Consequences

- The library becomes describable and therefore reviewable: settings shows the
  rules, the Library screen shows the result, and the run log connects them.
- The system principal arrives with this feature or the feature does not ship. It
  is shared with telemetry retention, the resolution-cache refresh, the
  watch-provider refresh and eventually torrent eviction, and it should be built
  once.
- Upstream load becomes predictable and continuous. That is better for a
  provider and worse for a shared quota, which is the cost
  [architecture#4](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0004-project-credentials-in-official-builds.md) also carries.
- A rule can produce a large library quickly, so the first run of a new rule is
  the one most likely to surprise its author. Bounding it and reporting what it
  will do before it does it is part of the surface, not a refinement.
- Stale stored attributes — the streaming-service grouping the register holds
  open — are discharged by the same scheduler, not by this record.
