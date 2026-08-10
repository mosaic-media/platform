# Content artwork is stored on the node

**Status:** Accepted, and **extended twice on its own reasoning**. M2b moved a
second value onto the node under this record's test — genres, because they are
*filtered* in bulk where artwork is *rendered* in bulk — and put a third,
streaming availability, in a table rather than on the node, because it has a
lifecycle the node does not (roadmap M2.4, M2.5). Neither is a reversal: this
record's argument is what decided both, in opposite directions. The follow-up it
names as owed — a command that updates a stored work's fields, so a re-import
refreshes stale artwork — is **still owed**; the availability refresh writes its
own projection and does not touch `Artwork`. The ref-reconstruction problem this
record documents as unsolvable from a node was **solved elsewhere**: [platform#62](0062-the-platform-keeps-what-a-source-told-it.md)
stores the provider's whole answer, `ContentMetadata.Ref` included, which is what
the M2.5 refresh re-asks with.
**Date:** 2026-07-23

## Context

[sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md) grew `ContentMetadata` into the full
descriptive surface a detail screen reads, and made a deliberate choice about
where a library item's detail comes from: it is **re-derived live** from the
provider on every view, not read from stored columns. That record weighed
storing the metadata on the `Node` at import and rejected it *for now* — "a
larger change than re-deriving from the provider, and it makes a library item's
detail only as fresh as its last import" — pairing a durable cache with
[platform#20](0020-artwork-proxy-and-cache.md)'s later work.

That reasoning is sound for the surface it was made against: **a detail screen is
one item and one live fetch, and always-current is worth a round-trip.** Two
things break it, and both arrive with slice 6's continue-watching rail
([platform#26](0026-playback-state-is-platform-owned.md)).

**A rail is a list, and re-deriving a list is a fetch per card per render.** The
home screen renders many in-progress items on every load. Re-deriving each card's
artwork is one live addon round-trip per card — the cost [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md) could accept
for a single detail becomes N of them on the landing page, and it makes the whole
rail only as available as the addon. The home screen is the wrong place to
inherit a provider's latency and uptime.

**And it is not actually reachable.** Re-deriving needs a provider-bearing ref —
`{Provider, NativeID, NativeType}` — to call `MetadataProvider`. A materialised
node cannot be turned back into one: the node and its source binding record the
external *scheme* (`imdb`) and id, but not the **module id** that served the
metadata, nor the provider's **native type** vocabulary (which is the module's
anti-corruption concern, [module-stremio-addons#2](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0002-modules-as-anti-corruption-layers.md),
and must not leak into the Platform). Every rich detail so far has been reached
from a search or catalog card that already carried the ref, so this never bit.
The rail is the first surface that starts from a node, and from a node the ref is
gone.

There is also a reason [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md) did not have. **Artwork is the one piece of
descriptive metadata a user may want to change** — to pick a different poster
than the source chose. That is only possible for something the library *owns*.
A value re-derived from a provider on every read cannot be overridden; a stored
one can.

## Decision

**A node's artwork — its poster, backdrop and logo URLs — is stored on the node
at materialisation, written through the existing content write path and read back
on the node. Only artwork crosses to storage; the rest of the descriptive
surface stays re-derived live per [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md).**

- **A small, closed, typed value.** `Artwork{Poster, Backdrop, Logo}`, URLs from
  the provider. It is not smuggled into the node's untyped `Attributes` document:
  that is per-media-type variation the schema does not validate
  ([platform#9](0009-object-graph.md)), and artwork is universal first-class
  display data a future feature will *write*. Putting it there would repeat the
  mistake [platform#26](0026-playback-state-is-platform-owned.md) refused for
  playback state — a place for a shape to hide from the schema. It gets its own
  column.
- **URLs, not bytes.** This stores the artwork's *addresses*. Caching the bytes
  behind them is [platform#20](0020-artwork-proxy-and-cache.md)'s proxy and its
  durable-cache follow-up, and stays that record's concern. The two compose: a
  stored URL is what the proxy is later asked to cache.
- **Only artwork, and deliberately so.** Textual metadata — cast, overview,
  rating, and the series episode preview — is rendered one item at a time on a
  detail screen and benefits from staying current, so it keeps re-deriving live.
  Artwork is singled out because it is the metadata that is both **rendered in
  bulk** on list surfaces and **a thing a user may override**. Those two
  pressures are what move it, and neither applies to the rest.
- **The provider's choice now; the user's choice later.** What is stored is the
  provider's primary URL per type. A later slice may fetch several candidates and
  let a user select among them — user-swappable artwork — growing this value into
  a candidate set with a selection. Storing artwork now is the precondition for
  that feature, not the feature itself.

## Alternatives considered

**Keep re-deriving live ([sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)'s status quo).** *Rejected.* It is unreachable
from a node without reconstructing a ref the Platform cannot reconstruct, and
where it is reachable it is a live provider round-trip per card on every render
of the landing page. The choice that was right for one detail is wrong for a
rail.

**Store artwork in the node's `Attributes` JSONB.** *Rejected.* No migration is
its only virtue. `Attributes` is unvalidated and is for per-media-type variation
([platform#9](0009-object-graph.md)); artwork is universal and will be written by a
user-facing feature. This is the "do not smuggle a typed shape into the untyped
document" reasoning [platform#26](0026-playback-state-is-platform-owned.md) already
applied — a dedicated, typed field is the honest form, and growing the store for
it is deliberate Platform evolution that should look like it
([platform#8](0008-capabilities-do-not-own-stores.md)).

**Store the whole rich metadata on the node** ([sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)'s rejected alternative,
in full). *Still rejected.* Cast, overview, rating and the episode preview are
fine re-derived and better for staying current; carrying all of it through the
write commands is the larger change [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md) named. Only artwork has the
bulk-render and user-override pressures that justify the crossing, so only
artwork crosses.

**Cache the bytes now instead of the URLs.** *Not taken here.* That is
[platform#20](0020-artwork-proxy-and-cache.md) slice 2 and orthogonal: it does not
give a node its artwork, it makes serving that artwork faster. This record is
about the node owning *which* artwork; that one is about delivering it.

## Consequences

- **The continue-watching rail renders from one node read.** No per-card addon
  call, and the rail's availability no longer tracks the metadata addon. Any
  future library grid inherits the same.
- **Stored artwork can go stale**, exactly as a snapshotted stream can
  ([platform#18](0018-virtual-and-materialized-content.md)): a URL that dies, or a
  source that later has better art, is not noticed on read. This is the
  freshness cost [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md) named, accepted for artwork because the always-current
  property matters far less for a poster than for a synopsis. It is **not yet
  self-healing**: the idempotent re-import refreshes an item's candidate
  *releases*, but there is no command that updates a stored work's fields, so a
  re-import does not refresh its artwork today. Adding that update path — which a
  user-overrides-artwork feature needs regardless — is the recorded follow-up;
  until it lands, refreshing stale art means removing and re-adding the item.
- **The store, the SDK and the module grow.** `Node` and the content write
  commands gain an `Artwork`; a migration adds the column; the Stremio module
  populates it from the metadata it already fetches. The content store set grows
  a column, not a store — smaller than [platform#26](0026-playback-state-is-platform-owned.md)'s
  new store, and the same deliberate-evolution rule.
- **User-swappable artwork becomes possible.** Because the node now owns its
  artwork, a later slice can let a user choose it and persist the choice —
  something a re-derived value could never allow. That is the forward reason for
  the crossing, beyond the rail that forced it.
- **[sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)'s dependence is narrowed, not removed.** A library detail's *artwork*
  no longer needs a reachable metadata addon; its *text* still re-derives and
  still does. The trade [sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md) recorded stands for everything this record does
  not name.

## Implementation implications

SDK: an `Artwork` type and an `Artwork` field on `AddContentWorkCommand` (and
`AddContentChildCommand`, so a materialised episode can carry its still), read
back on `Node`; a minor `v0.x` bump. Platform: a migration adding an `artwork`
`jsonb` column to `nodes`, the node store reading and writing it, and the
add-work / add-child handlers carrying it through unchanged command order.
Stremio module: fill `Artwork` on import from the `Poster`, `Background` and
`Logo` it already decodes ([sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)), a minor
bump. Emit-side: the continue-watching rail reads the in-progress node's Work
artwork directly. Cross-repo version coordination follows the usual flow — tag
the SDK and module, bump the requires, and the local `replace` directives bridge
it until the owner publishes.
