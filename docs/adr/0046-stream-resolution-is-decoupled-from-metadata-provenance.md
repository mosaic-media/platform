# Stream resolution is decoupled from metadata provenance

**Status:** Accepted
**Date:** 2026-07-23

Amends [platform#18](0018-virtual-and-materialized-content.md)'s single-capability
import path and closes the seam [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md)
left open. Depends on [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)'s
provider roles.

## Context

`ImportContent` resolves exactly one capability and hands the whole
materialisation to it:

```go
capability, ok := s.lookupCapability(cmd.Ref.Provider)
```

That was correct when one module both described content and indexed it. It is
not correct now, and the reason is the two core metadata modules.

**`module-tmdb` and `module-cinemeta` fill no stream role, deliberately.** They
describe content; they do not host or index it. So a title materialised from a
TMDB search result is a Work and a season/episode tree with **no Parts** — and
`module-remote-playback`, which turns a Part into bytes, has nothing to turn.
The item is in the library and cannot be played, ever, no matter what stream
sources the deployment has installed. `module-stremio-addons` is registered, it
can resolve that exact title, and it is never asked, because the ref says
`tmdb`.

The consequence is backwards in a way worth stating plainly: **it couples the
quality of a user's metadata to whoever happens to index the streams.** To get
a playable library you must import through Stremio, which means accepting
Cinemeta-grade descriptions — no clearlogo, no cast photographs, no franchise —
even with a richer provider installed and configured. The provider-role split
([sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)) exists precisely
because describing and indexing are different jobs, and the import path was
still treating them as one.

[module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md) named
this and did not close it: *"neither core metadata module attaches a Part, and
the Platform cannot bridge that — `ImportContent` routes solely to
`ref.Provider`, so a stream provider that did not source the metadata never gets
asked."*

What has changed since is that the bridge now has something to stand on. The
metadata modules bind their works under a **shared external identity** — TMDB
binds `imdb` (and `tvdb` for series) alongside its own id, and Cinemeta's ids
*are* IMDb ids. There is now a key both sides recognise.

## Decision

**Materialising a virtual item is two steps, not one. The capability the ref
names builds the tree; the Platform then asks every registered stream provider
to resolve playable locations for the items it created, addressing them by a
neutral identity rather than by a provider's own.**

- **The materialiser is unchanged.** The ref still names exactly one capability
  and that capability still owns the tree it builds ([platform#18](0018-virtual-and-materialized-content.md)).
  A module that attaches its own Parts — Stremio importing a Stremio ref — keeps
  doing so, and the enrichment pass adds to that rather than replacing it.
- **The Platform runs the enrichment, not the module.** A module cannot call
  another module: the SDK hands it a `ContentService` and nothing else, and
  [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md) keeps provider
  resolution Platform-side. A module-to-module call would also make modules
  depend on one another, which is the coupling the registry exists to prevent.
- **A stream provider is addressed by neutral coordinates.** The request carries
  the work's **shared external identity** (`imdb`, `tvdb`) and, for an episode,
  its **season and episode number** — not a native id. The module derives its
  own addressing from those.

  This is the load-bearing half. Stremio's native episode id is `tt0903747:1:2`,
  a format only Stremio knows; a Platform that built that string would have a
  provider's dialect in the kernel, which [module-stremio-addons#2](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0002-modules-as-anti-corruption-layers.md)
  forbids in as many words. Season and episode are facts about television that
  the Platform already models; the string is the module's business.
- **Enrichment is best-effort and never fails an import.** A stream provider
  that is unreachable, unconfigured, or simply does not know the title yields
  nothing, and the import succeeds with the tree it already has. An item with no
  Parts is a valid outcome — it is what a metadata-only deployment produces —
  so "no streams" must not be an error, and a source being down must not lose a
  user the work they just added.
- **A provider is asked only about identities it can use.** With no shared
  external id on the work there is nothing neutral to send, and the pass is
  skipped rather than guessed at.

## Alternatives considered

**Let the metadata module call the stream module.** *Rejected*, and it is the
intuitive design. The SDK gives a capability no way to reach the registry, and
adding one would make a module depend on another module's presence, id and
behaviour — the registry exists so that modules compose without knowing about
each other. It would also put the fan-out inside every metadata module that
wanted it, duplicated and slightly different each time.

**Have the Platform construct each provider's native id.** *Rejected.* It is
the shortest path and it puts `tt0903747:1:2` in Platform code. Every source
added afterwards would add another format to a switch statement in the kernel,
which is exactly the anti-corruption inversion [module-stremio-addons#2](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0002-modules-as-anti-corruption-layers.md)
exists to prevent.

**Make the metadata modules fetch streams themselves.** *Rejected.* TMDB has no
streams to fetch. It would turn every metadata module into a stream aggregator,
and it re-couples the two concerns the role split separated.

**Leave it, and require importing through a stream source.** *Rejected* — this
is the status quo, and it means the core metadata modules can never be the way
a user actually adds content. It would make [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md)'s
guaranteed provider a thing that describes a library nobody can build.

**Resolve streams lazily at play time instead of at import.** *Not rejected —
deferred, and it is probably where this ends up.* A snapshotted stream location
is perishable ([platform#28](0028-resolution-cache-and-capability-classes.md)
already caches resolution separately), and asking at play time would always be
current. But playback resolves a `Part`, so with no Part there is nothing to
play and nothing to resolve; making the Part optional is a larger change to the
playback path than this record wants to make. Import-time enrichment is
compatible with it: the Parts it writes are candidates, which is what they
already are.

## Consequences

- **A title described by TMDB and streamed by Stremio is now the normal case**,
  which is what the two-tier module story has been pointing at since
  [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md). Rich metadata no longer costs you
  playback.
- **Import gets slower and noisier.** It was one module's work and is now one
  module's work plus a fan-out over every stream provider, per item. For a
  series that is a request per episode per provider. The bound on it is the
  candidate cap each provider already applies, and it is a real cost paid at
  import rather than at play.
- **A stream provider is now asked about content it did not source.** Empty is
  the expected answer much of the time and must stay cheap and silent. This also
  makes a provider's own dedup its problem: nothing stops two providers
  returning the same release, and **cross-provider Part dedup is left open** —
  the same open seam as provider precedence, now reachable from a second
  direction.
- **The shared external identity becomes load-bearing.** It was a
  nice-to-have for dedup and is now the thing the bridge stands on: a metadata
  module that binds only its own scheme produces content nothing can enrich.
  That is worth stating as an expectation of a metadata module rather than
  leaving it to be discovered.
- **`StreamRequest` grows season and episode.** A small additive SDK change, and
  the second time a module has forced one by being unable to express something
  ([platform#17](0017-module-settings.md) settings was the first).
- **Nothing here helps a work with no shared id.** A source keyed only to itself
  is unenrichable by design rather than by oversight, and a deployment running
  only such sources sees exactly today's behaviour.
