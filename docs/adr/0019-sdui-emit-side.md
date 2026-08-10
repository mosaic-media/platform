# The Platform's SDUI emit-side

**Status:** Accepted
**Date:** 2026-07-20

## Context

The client half of the server-driven interface is built: the Shell renders a
tree of `UINode`s carrying `Action` envelopes ([contracts#1](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0001-server-driven-ui-and-the-shell.md),
[contracts#2](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0002-primitives-and-definitions.md)), the contract is published with
a Go producer binding ([contracts#3](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0003-sdui-contract-repository.md)), and the
React runtime renders it ([web#1](https://github.com/mosaic-media/web/blob/main/docs/adr/0001-react-sdui-runtime.md)). But the
**Platform emits no screens** — the Shell runs on mock payloads, because nothing
on the server builds a `UINode` tree from real state.

The provider-model backend ([sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md),
[platform#18](0018-virtual-and-materialized-content.md)) just made two screens
worth emitting real: a **user search screen** (search with no raw id) and an
**admin collection browser** (browse a module's catalogs, publish a selection).
The GraphQL those screens need — `searchAvailableContent`, `moduleCatalogs`,
`catalogItems`, `importContent` — exists. What is missing is the server building
the screens and a transport that serves them.

A `UINode`'s `Action` references a Platform operation *by name*: a `Query` action
names a query to run, an `Invoke` action names a mutation. So the emit-side is
not just "build a tree" — it is how screens are served *and* how their actions
bind to the operations that already exist.

## Decision

**The Platform emits screens through a GraphQL `screen(name, params)` query that
returns a `UINode` tree, built with the `mosaic-sdui` Go producer binding by a
Platform-owned screen registry. A screen's actions name the Platform's own
GraphQL operations.**

- **One transport, GraphQL.** `screen(name: String!, params: JSON): JSON`
  returns the serialized `UINode` tree. The Shell already speaks GraphQL, and a
  screen's `Query`/`Invoke` actions name GraphQL operations — so screens and the
  data their actions drive share one transport, rather than a screens endpoint
  that references operations on a different one. A dedicated `/screens` endpoint
  was the alternative; it splits the surface for no gain here.
- **A Platform-owned screen registry.** `name → builder(ctx, caller, params) →
  sdui.Node`, a closed set the Platform owns: `search` and `collections` now,
  `library`/`detail`/`playback` as they are built. The builder calls application
  **query services only** — the same rule GraphQL resolvers follow ([platform#12](0012-published-contract-surface.md)):
  a screen is a projection, never a persistence path, and never touches a store.
- **Screens are Platform-emitted; modules contribute data, not UI.** A module
  imports only the SDK and cannot import the SDUI binding or a Platform transport
  ([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)), so it does not emit
  screens. The Platform composes a screen from module *data* — the provider
  results of [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md) — not from
  module-supplied `UINode`s. A module extends *what content exists*; the Platform
  owns *how it is shown*.
- **Actions bind to the slice-3 operations.** Every result card is a `Navigate`
  to a **detail** screen — a virtual item carrying its `ContentRef`, an in-library
  item its node id. Materialising is not a card click but the *Add to library*
  action on a virtual item's detail (`Invoke: importContent`), so a virtual item
  is explored before it is added; the `search` chrome drives a fresh search. The
  collection browser lists `moduleCatalogs`/`catalogItems`; publishing a selection
  is the same detail-then-add path.
  An action-invoked mutation both **accepts** its argument as `input: JSON` and
  **returns a scalar**: the runtime issues `mutation { name(input: $input) }` with
  no sub-selection, which a field returning an object type would require. So
  `importContent` returns the JSON scalar carrying its result, not an object type.
- **The virtual/materialized split is visible.** A result carries `InLibrary`
  ([platform#18](0018-virtual-and-materialized-content.md)); the builder renders an
  in-library item with a badge and its node detail, a virtual one as a metadata
  preview (read through the module's `MetadataProvider` via a `PreviewContent`
  query) whose only library affordance is *Add to library* — so the two planes
  read as one list without the user needing to know the model.
- **Auth is uniform.** `screen` carries the caller like every other query; each
  builder authorises through the services it calls, so a screen shows exactly
  what its caller may see.

## Alternatives considered

**A dedicated `/screens` HTTP endpoint, separate from the GraphQL data API.**
*Rejected:* a screen's actions reference GraphQL operations regardless, so a
second transport would straddle the two. One surface keeps screens and the
operations they invoke aligned.

**Modules emit their own screens.** *Rejected:* a module imports only the SDK and
cannot import the SDUI binding or a Platform transport ([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)),
the same boundary that made `importContent` a generic Platform surface ([platform#15](0015-module-capability-and-invocation.md)).
The Platform composes screens from module data. (A future module-contributed-UI
story, if one is ever wanted, is its own decision, not this one.)

**The Shell builds `UINode`s from raw GraphQL data itself (a thin client
adapter).** *Rejected:* it bypasses the whole point of server-driven UI — that
the server owns the screen and a second client (a future Flutter app) renders
the same payload ([contracts#1](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0001-server-driven-ui-and-the-shell.md), [contracts#2](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0002-primitives-and-definitions.md)).
The screen must be built once, on the server.

## Consequences

The Shell stops running on mock payloads for these two screens — the first real
server-emitted screens, and the first time the extension backend is reachable by
a human through the interface rather than through raw GraphQL. The screen
registry is the seam every later screen slots into.

Three honest limits:

1. **Region-refresh protocol.** A `Query` action refreshing a named region (the
   search grid) needs the Shell and the emit-side to agree on how a partial
   result replaces a region. The contract has the `Into` field ([contracts#3](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0003-sdui-contract-repository.md));
   the end-to-end refresh is exercised for the first time here and may surface
   contract gaps — the same forcing-function role modules play for the SDK.
2. **Modules cannot contribute UI.** Deliberate ([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)),
   but it means a module that wants a bespoke configuration screen has none; the
   Platform renders module settings generically. Named, not solved.
3. **The skin is still neutral.** These screens render on the Shell's neutral
   token skin; the Mosaic Design Language ([roadmap](../roadmap.md)) that replaces
   it is separate work.

## Implementation implications

The Platform gains a `go get github.com/mosaic-media/mosaic-sdui` dependency and
a screen package that builds `sdui.Node` trees from the application query
services, plus the `screen` GraphQL query over a registry. The two builders
(`search`, `collections`) read the slice-3 services and emit the standard
component vocabulary ([contracts#2](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0002-primitives-and-definitions.md)) — `Screen`,
`SearchBar`, `Grid`, `PosterCard`, `EmptyState`, `Button`. The Shell then points
those two routes at `screen(...)` instead of its mock payloads. Building the
screens is expected to pressure-test the `Action`/region contract, and any gap
found is a `mosaic-sdui` change, tagged and consumed like any other.
