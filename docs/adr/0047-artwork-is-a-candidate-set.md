# Artwork is a candidate set

**Status:** Accepted; **built, except the user's half of the selection rule.**
`v1.Artwork` carries `Candidates` beside the four flat slots, `ArtworkSlot` is
the open eight-value vocabulary this record names, and an `ArtworkCandidate`
carries `{Slot, URL, Source, Language, Rank}`. Selection resolves on write in
`enrich_artwork.go`: candidates union across providers, dedupe, sort textless
first for backdrop and landscape, then by the source's own rank, then by source,
capped at twelve per slot — and the winner's URL is written into the flat slot,
so no reader walks the list. **Two parts of the stated rule are not built.**
There is no "prefer the user's configured language" step — the sort reads
language only to find the textless ones — and there is no picker, so nothing
records a user's choice or outranks the rule. That second one is the thing this
record says it exists to make possible, and it is the half still owed.
**Date:** 2026-07-24

Grows the value [platform#45](0045-content-artwork-is-stored-on-the-node.md) put on
the node, and delivers the selection half
[platform#20](0020-artwork-proxy-and-cache.md) deferred to its slice 2. Closes one
of [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)'s recorded gaps. The role that
supplies the candidates is [sdk#6](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0006-the-artwork-provider-role.md).

## Context

`v1.Artwork` holds **one string per slot** — `Poster`, `Landscape`, `Backdrop`,
`Logo`. That is what a node stores and what every surface renders. Three records
have now named the same limit from three directions, and none of them closed it:

- [platform#20](0020-artwork-proxy-and-cache.md): *"Candidate selection is slice 2.
  Slice 1 proxies the single poster the module already returns; picking the best
  of several candidates lands with the durable cache and a small SDK addition."*
- [platform#45](0045-content-artwork-is-stored-on-the-node.md): *"What is stored is
  the provider's primary URL per type. A later slice may fetch several candidates
  and let a user select among them."*
- The roadmap, on the TMDB module: *"the `images` response carries every poster
  and backdrop variant and `v1.Artwork` holds one string per slot — [platform#45](0045-content-artwork-is-stored-on-the-node.md)
  anticipates a candidate set, but changing what a stored artwork value *means*
  is an ADR rather than a field."*

That last sentence is why this is a record rather than a commit. **The change is
not "add a field", it is "change what the value means"** — from *the* artwork to
*the selected* artwork out of a set.

What forces it now is a source whose entire value is the set. A fanart.tv-class
provider does not return a poster; it returns forty of them, each with a
language, a vote count, and a type vocabulary far wider than four slots.
Discarding thirty-nine and keeping one throws away the reason to install it. The
TMDB module already discards its own `images` response for exactly this reason,
and has recorded it as a gap since it was written.

There is a second, sharper reason, and [platform#45](0045-content-artwork-is-stored-on-the-node.md) already stated it: **artwork is
the one piece of descriptive metadata a user may want to change.** A stored
single value can be overwritten but not *chosen from*. Choice needs alternatives
to choose between, and there is nowhere to put them.

### One constraint is already settled

Artwork is a `jsonb` column, and migration `0019_node_artwork.sql` said why, in
advance:

> A jsonb document rather than three text columns: it matches `external_ids` and
> `attributes` beside it, holds the provider's primary URL per type today, and
> **leaves room for a candidate set and a user selection later without a second
> migration.**

So the storage question is not open. What is open is the *shape*, and the shape
is constrained by every row already written: a node stored today holds
`{"poster":"…","backdrop":"…"}` and must keep reading correctly after this lands.

## Decision

**`Artwork` grows a set of candidates alongside its existing slots. The flat
slots stay, and their meaning narrows from "the artwork" to "the selected
artwork" — a resolved view of the set. Every existing reader and every existing
row keeps working, unchanged.**

- **The flat slots are the resolved view, not a legacy shim.** `Poster`,
  `Landscape`, `Backdrop` and `Logo` continue to hold one URL each, and they
  remain what a renderer reads. What changes is who writes them: today a module
  writes its only value; after this, selection writes the chosen candidate's URL
  into the slot it fills. A screen never walks the candidate list, and no emit-side
  code changes at all.

  This is the load-bearing choice of the record. The alternative — replace the
  slots with a list and an index — makes every consumer resolve the selection
  itself, and each one would do it slightly differently. Resolving once on write
  means there is exactly one answer to "what is this node's poster".

- **Candidates carry provenance, language and the source's own ranking.** A
  candidate is `{Slot, URL, Source, Language, Rank}`. `Source` is the module id
  that supplied it, so a set assembled from two providers stays attributable and
  a later "prefer fanart.tv over TMDB" preference has something to key on.
  `Language` matters more than it looks: a *textless* backdrop is the correct
  one to sit behind a clearlogo, and it is distinguishable only by its language
  being absent. `Rank` is the source's own ordering — vote counts, popularity —
  normalised to nothing, because the scales are not comparable across sources
  and inventing a common one would be a lie the store then persists.

- **Candidates are additive across providers; slots are not.** Two providers'
  candidates union into one set, because more choice is strictly better and
  nothing has to stay internally consistent between two entries in a list. The
  *slots* are the opposite — the resolved view is chosen, one per slot, and a
  regional poster must not end up beside another source's English logo. This is
  the tiered rule `module-stremio-addons` already applies when merging several
  addons' answers, applied one level up.

- **Selection is a rule now and a user choice later.** The Platform resolves
  each slot from the candidates by a stated, boring rule — prefer the user's
  configured language, prefer textless for backdrop and landscape, then the
  source's own rank, then provider order. **The rule is not the feature; having
  somewhere to record a choice is.** A user picking a poster writes a selection
  that outranks the rule, and that is the thing this record exists to make
  possible. Shipping the rule without the picker is a real improvement on its
  own — the best-ranked textless backdrop beats whatever the metadata provider
  happened to list first — and it is not the whole of it.

- **The slot vocabulary grows, and it is open text with known values**
  ([platform#11](0011-open-and-closed-vocabularies.md)). Poster, landscape,
  backdrop and logo become four of a longer list — banner, clearart, disc,
  characterart — which is what closes
  [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)'s *"not built — no data in the
  source"* entry for clearart and banners. It is open because a source will have
  a type Mosaic has not heard of, and dropping it on the floor is worse than
  carrying it: a consumer that does not recognise a slot ignores it, exactly as
  it ignores an unrecognised media type. **Only the four existing slots get a
  flat field**; the rest are reachable through the candidate set, because adding
  a flat field per art type is how a struct becomes a bag.

- **Season artwork needs nothing new.** A season is a `ContainerSeason` node and
  `Artwork` is stored per node, so a season poster is a season node's poster. The
  shape already fits; it was simply never filled.

## Alternatives considered

**Leave `Artwork` flat and let the module pick.** *Rejected — and it is the
cheap option that nearly works.* A module choosing its own best candidate does
deliver better images today with no contract change at all. What it cannot ever
deliver is a *user's* choice, because the alternatives never leave the module's
address space, and it makes every provider re-implement a selection heuristic
privately and differently. [platform#45](0045-content-artwork-is-stored-on-the-node.md) named user-swappable artwork as the forward
reason for storing artwork at all; this alternative forecloses it permanently.

**Replace the flat slots with a candidate list plus a selection index.**
*Rejected.* It is the tidier data model and it is worse everywhere it is read.
Every existing row would need rewriting (against migration 0019's stated
expectation), every consumer — the continue-watching rail, the hero, the search
result card — would resolve the selection itself, and "what is this node's
poster" would stop having one answer. Keeping the resolved slots is what makes
this change additive rather than a migration of the whole read side.

**Store candidates in the node's `Attributes` document.** *Rejected*, for the
reason [platform#45](0045-content-artwork-is-stored-on-the-node.md) already
rejected putting artwork there: `Attributes` is unvalidated and exists for
per-media-type variation, and this is universal display data a user-facing
feature will write. The dedicated column exists precisely so this shape does not
have to hide.

**A separate `artwork_candidates` table.** *Rejected for now.* It is the right
answer if candidates ever need to be queried across nodes — "every poster
fanart.tv supplied", for a bulk re-selection screen — and nothing wants that.
Until something does, a row per candidate buys a join on the hot path of every
list surface in exchange for a query nobody issues.
[platform#8](0008-capabilities-do-not-own-stores.md)'s rule holds either way: this
would be the Platform's store, not a module's.

**Normalise every source's rank onto a common 0–1 score.** *Rejected.*
fanart.tv's likes, TMDB's vote average and an addon's list order measure
different things with different populations. A blended number reads as
authoritative and is not, and the store would persist the invention. Carrying
each source's own rank and preferring within a source is honest and sufficient.

## Consequences

- **Better art before any UI is built.** Choosing the best-ranked textless
  backdrop and a language-matched poster is a visible improvement over "whatever
  the metadata provider listed first", and it lands with the selection rule
  alone.
- **No migration, as migration 0019 predicted.** The document grows a key; rows
  written before this read as a set with no candidates and their existing slots
  intact, which is exactly "the source gave one and it is selected".
- **The SDK bumps additively.** A `Candidate` type, a `Candidates []Candidate`
  field, and a slot vocabulary. No existing field changes type or meaning for a
  reader — only for a writer.
- **Stored candidates go stale like stored slots do.** [platform#45](0045-content-artwork-is-stored-on-the-node.md) accepted this
  for artwork and it now applies to more URLs. The same missing refresh path is
  the same recorded follow-up, and it is now more clearly owed: a re-import that
  cannot refresh artwork also cannot pick up art uploaded since.
- **The set is unbounded unless something bounds it.** Forty posters per node
  across several providers, on every node, is a real storage and payload cost
  for a document read on every list render. **A cap belongs in this design and is
  named here rather than discovered later:** the Platform keeps a bounded number
  of candidates per slot, best-ranked first, and says so where it truncates.
- **Two providers can now supply the same slot, which is provider precedence
  arriving by a third route.** [platform#23](0023-metadata-as-required-capability.md)
  and [platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)
  both left it open. For artwork specifically the candidate set **dissolves** it:
  there is no winner to pick, both sets are kept with provenance, and selection
  is a per-slot resolution rather than a per-provider ranking. That is not a
  general answer — streams still need one — but it is the correct answer here.
- **The picker is not built by this record**, and a capability with no client
  path is [owed](../unreachable-capability.md) rather than done. Selection
  resolving by rule is reachable; a user overriding it is not, until a screen
  exists to do it on.

## Implementation implications

**SDK** (minor `v0.x` bump): `artwork.go` gains an `ArtworkSlot` open vocabulary
with the eight known values, a `Candidate{Slot, URL, Source, Language, Rank}`,
and `Candidates []Candidate` on `Artwork`. `Empty()` accounts for candidates.
Helpers to resolve a slot from a set live here rather than in each consumer.

**Platform**: no migration. The node store already marshals `Artwork` whole. The
selection rule and the candidate cap live beside the enrichment pass
([sdk#6](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0006-the-artwork-provider-role.md)); the emit-side is untouched
because it reads the resolved slots it already reads.

**Modules**: `module-tmdb` stops discarding its `images` response and contributes
candidates, which is the gap its README has carried since it was written.
`module-cinemeta` and `module-stremio-addons` are unchanged — a source with one
poster supplies one candidate, and that candidate is selected.
