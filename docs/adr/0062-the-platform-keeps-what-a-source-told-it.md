# The Platform keeps what a source told it, and tops up the tree

**Status:** Accepted and built in roadmap M2a, discharging M2.7's durable
metadata cache. Amends [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)'s "re-derived
on every render" and extends [platform#18](0018-virtual-and-materialized-content.md)'s
single materialising capability the same way
[platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md) and
[sdk#6](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0006-the-artwork-provider-role.md) already did.
**Date:** 2026-07-27

## Context

Two failures with one cause, both found by opening a library the M2a maintenance
job had filled.

**A library item's detail screen was blank.** [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)
decided that detail is re-derived live from the provider on every render — "as
current as the source, at the cost of needing a reachable metadata addon" — and
that read is keyed by a **ref**. A card on the Library screen opens its node by
**id**, because that is the whole point of a screen over the object graph: it
must still open when the source is down. So the two do not meet. The node-id
path fell back to a structural view — a title, a media type, and its children as
cards with no artwork — and that is what somebody browsing their own library got.

The obvious repair is to send the ref along with the id. It is the wrong one: it
makes every library detail a live provider call, which is precisely what M2.7
already names as a defect. With one user that is freshness; with four on a
credential shared by every default install ([architecture#4](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0004-project-credentials-in-official-builds.md))
it is latency and failure, and now a rule-built library of several hundred
titles makes it a browse that cannot be paid for.

**A series that gained a season never grew.** Every module dedups before writing
and returns `AlreadyKnown` for a title it has already materialised, so the
maintenance pass ([platform#60](0060-the-library-is-built-from-rules.md)) re-merged
artwork and topped up Parts and never extended the tree. That is the *first*
example [platform#60](0060-the-library-is-built-from-rules.md) gives of what a self-maintaining library is for, and it did not
work. A household that follows a running series gets the seasons that existed the
day the rule first ran, forever.

Both come from the same place: **the Platform asks a provider a question,
renders the answer, and keeps none of it.**

## Decision

**What a metadata provider says about a materialised title is stored, refreshed
by the maintenance pass, and read from storage thereafter. The same pass uses
what it was told about episodes to add the parts of the tree that are missing.**

**Stored in a table of its own, not on the node.** `node_metadata` is one
document per node with the time it was fetched. [platform#45](0045-content-artwork-is-stored-on-the-node.md)
put artwork *on* the node and the reasoning does not carry over: artwork is
chosen per node and read on every card of every list, so a join would be on the
hot path, whereas a metadata document is read on one screen at a time and would
otherwise be carried through every list read that never looks at it. It also has
a lifecycle the node does not — a fetched-at, a staleness question, and a
retention question — and those belong to the cache rather than to the content.

**It is Platform-owned and not a module's `attributes`.** `attributes` is
module-owned and the Platform never interprets it ([platform#9](0009-object-graph.md));
this document is read by the Platform to draw a screen, which is the opposite
contract. Storing it there would make the emit-side depend on a document any
module may write anything into.

**The Platform writes it, not the module.** It is a third enrichment pass beside
streams and artwork, and it is there for the reason those are: the module that
materialised a title is named by the ref, the Platform is the one that knows
which providers are registered, and a pass costs no SDK change and no change in
any module. Best-effort by construction, like its two neighbours — a provider
that is down leaves the last document in place, which is a cache doing its job.

**The tree top-up builds from season and episode numbers, and nothing else.**
`ContentMetadata.Episodes` is a read projection the UI groups by season, and this
now also reconciles it: a season number with no container gets one, an episode
number with no item gets one, and anything already there is left alone. That is
the Platform building a tree, which [platform#18](0018-virtual-and-materialized-content.md)
gave to the materialising capability — and the ground for taking part of it back
is exactly [platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)'s:
**season and episode are facts about television that the Platform already
models.** What it must not do is compose a provider's own addressing; it does
not, because it composes nothing — it reads two integers the SDK already carries
neutrally.

**It adds and never removes**, for the same reason rules do. An episode that
disappears from a source's listing stays: a source's churn is not a household's
decision, and an episode somebody is part-way through is the worst possible
thing to delete.

## Alternatives considered

**Carry the ref alongside the node id on every library card.** *Rejected.* It is
the smallest change and it makes every detail render a provider round trip, which
is the defect M2.7 exists to remove. It also re-couples a library screen to a
source being reachable, which is the property the object graph is for.

**Store the document on the node** (a `metadata` column beside `artwork`).
*Rejected*, and it was the first design. Every list read already carries the
node, so the document would ride along on reads that never open it, and the
cache's own lifecycle would be tangled with the content's.

**Let each module store its own metadata through a new SDK command.**
*Rejected.* It is a published-surface change, a release of every module, and it
puts the same code in each of them — the argument
[platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)
already made against module-to-module fan-out, applied to storage.

**Have the module rebuild its own tree on re-import.** *Not rejected — deferred,
and it is the better answer.* A module knows its own source's shape and could
reconcile precisely, where this reconciles from a projection. It needs a refresh
verb on the SDK's `Capability` and a release of every module, and until that
exists a series that gains a season is a thing a household notices and cannot
fix. This is the cheaper repair, and the seam is recorded rather than closed.

**Cache with a TTL and re-fetch on read.** *Rejected.* It puts a provider call
back on the render path at exactly the moments it is least wanted — the first
open after a quiet night — and Mosaic already has the thing that should own
refreshing: a scheduled pass that is bounded and reports what it did.

## Consequences

- **A library detail renders from the graph.** It opens with no metadata provider
  installed, no credential, and no network, which is what an object graph is for.
  A *virtual* item's detail is unchanged and still live by ref — the two planes
  read differently now, and that is the honest difference between something the
  install owns and something it is looking at.
- **The maintenance pass now genuinely refreshes.** "Refreshed" in a rule's run
  log means the document was re-read, the artwork re-merged, the Parts topped up
  and any new episodes added, rather than only the last three.
- **The cache can be wrong in a way the live read could not.** A title renamed at
  the source shows its old name until the next run. That is the trade every cache
  makes, and the run log is where the answer to "why does it still say that" is.
- **A metadata provider is now asked about content on a schedule** rather than
  when somebody looks. That is the load shift [platform#60](0060-the-library-is-built-from-rules.md)
  already accepted, and it is bounded by the same budget.
- **Episodes gain nodes, so the graph grows faster than the library does.** A
  household following four running series accumulates episode rows every week
  whether or not anything plays them. That is the correct shape — an episode is a
  thing you can watch and record progress against — and it is worth stating
  because the object graph's size was previously governed by titles alone.
