# Capability-gated affordances and consumer roles

**Status:** Accepted; **built in part, and the gate reads something else than
this record specifies.** The consumer class is real: `RolePlayback` is in the
SDK, `module-remote-playback` fills it
([platform#25](0025-playback-consumer-and-media-origin.md)), and
`CapabilityRegistry.PlaybackProviders()` enumerates it. **What was not built is
the gate itself.** Nothing on the emit side reads the installed capability set —
`PlaybackProviders()` has one caller, `ResolvePlayback`, at resolution time, and
the screens layer has no handle on the registry at all. Play and Add are gated
on the *caller's* `content.import` permission
([platform#44](0044-privilege-cannot-escalate.md)), and the continue-watching rail
on there being something in progress; both cite this record, and both are
proxies for it rather than it. So there is no "discovery mode": an install with
no consumer draws Add and Play for anyone who may import, which is the dead-end
affordance this record exists to prevent. The "gate on a consumer existing, not
on playback existing" distinction is therefore untested by anything.
**Date:** 2026-07-21

## Context

"Add to library" today materialises a Work and snapshots a stream location into
a `RemoteLocation` Part ([platform#18](0018-virtual-and-materialized-content.md)) —
but there is no playback module, so nothing can resolve or play those bytes
(play-time resolution is the deferred **Remote Media** module). The materialised
library is **inert**: a user can add content they can then do nothing with. That
is a dead-end affordance, and it will read as a bug the first time someone adds a
film and finds no way to watch it.

The deeper issue is that every provider role in the model
([sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md) —
`Metadata`/`Search`/`Catalog`/`Stream`) is a **source** role: they bring content
*in* and populate the virtual plane. Even `Stream` is "here is a location," not
"I can play it." There is **no role representing a consumer** of the materialised
library — nothing that *uses* what materialising creates.

There is a product principle worth making explicit: **the functionality Mosaic
offers is a function of the capabilities installed.** A base install (Platform +
storage + a metadata source, [platform#23](0023-metadata-as-required-capability.md))
should offer discovery — search, browse, preview. Materialising and playing
should light up only when something can *consume* a library.

## Decision

**Introduce consumer capabilities — roles that use materialised library state,
distinct from the source roles that populate the virtual plane — and gate
library affordances on their presence. The Platform's emit-side renders
add/play only when a capable module is installed; with only source roles present,
the surface is discovery-only.**

- **Consumer capabilities** are the sink-side counterpart to
  [sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)'s source roles. The
  first is **playback** (with remote and local variants — the Remote Media module
  and a future local-file player). **Request** (acquire content Mosaic does not
  hold) and **export** (NFO/`.mos`) are later consumers of the same class. A
  source *produces* virtual results; a consumer *acts on* the materialised graph.
- **Gating is keyed on the installed capability set**, read through the
  `CapabilityRegistry`:
  - **Play** is offered only when a **playback provider** is installed.
  - **Add to library (materialise)** is offered only when **at least one library
    consumer** is installed — playback being the first, request and export also
    qualifying.
  - With only source roles present (today: Stremio alone), the surface is
    **discovery mode** — search, browse, metadata preview — and the emit-side
    does not render add or play. Where useful it renders an explanatory state
    ("install a playback module to build a library") rather than a disabled
    button with no explanation.
- **Gate on "a consumer exists," not "playback exists."** A metadata-only
  enrichment or export deployment is a real case
  ([platform#17](0017-module-settings.md) already notes meta-only imports that
  enrich local media). Playback is the *first* consumer, not the definition of
  the gate — keying on playback specifically would wrongly make "library implies
  streaming."
- **Gating lives on the server** ([platform#19](0019-sdui-emit-side.md),
  [contracts#1](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0001-server-driven-ui-and-the-shell.md)). The Platform owns how
  content is shown and knows the installed capabilities; the emit-side simply
  omits affordances a consumer would drive. Modules contribute capabilities, not
  UI. This sits cleanly on the virtual/materialised split
  ([platform#18](0018-virtual-and-materialized-content.md)): browsing is always
  available because it is a read; materialising is gated because it creates state
  only a consumer can use.

## Alternatives considered

**Always allow Add-to-library (status quo).** *Rejected.* It creates inert
library state — content a user can add but nothing can use — which is a dead end
and reads as broken.

**Gate specifically on playback.** *Rejected as too narrow.* It forecloses
metadata-only enrichment and export-only deployments and bakes in "a library is
for streaming," which is not true. "A consumer exists" is the right predicate;
playback is one instance.

**Gate on the client (the Shell decides what to show).** *Rejected.* It violates
the SDUI thesis — the server owns the interface, and a second client would
re-implement the rule. The Platform knows the installed capabilities; the client
must not encode this.

**A user-toggled capability flag in the manifest.** *Deferred.* The manifest
shape is still growing ([platform#11](0011-open-and-closed-vocabularies.md),
[platform#15](0015-module-capability-and-invocation.md)); presence-of-role is a
sufficient and simpler signal for now.

## Consequences

- **Honest, progressive UX.** A base install is a discovery tool; installing a
  playback (or other consumer) module unlocks the library and playback. No
  dead-end affordances.
- **The role vocabulary grows a sink side.** This is the first *consumer* role,
  and it is invoked differently from a source: it acts *on* the graph rather than
  producing virtual results, which touches the same "whose authority" question
  the source roles largely dodged — the **system principal** and cross-module
  authority seams ([platform#13](0013-how-a-capability-acts.md)) become live for a
  consumer that acts in the background.
- **The gate is buildable before any consumer exists.** Defining the consumer
  role and gating on its absence is immediately correct — today the answer is
  always "no consumer," so the surface is discovery-only, which is exactly right.
  The concrete playback *provider interface* and the Remote Media module are the
  larger follow-on; the gate does not wait on them.
- **Request and export converge here.** The roadmap's request and export items
  become instances of the consumer concept rather than separate one-offs.
- **A small emit-side change** (read the registry, omit affordances, render a
  discovery-mode state) — it already routes through the registry.

## Implementation implications

Define the consumer/playback role(s) in the SDK role vocabulary (extending
[sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md) from source-only to
source + consumer); the *first implementation* (Remote Media) is deferred, so the
role is defined and gated-on before a module fills it — which is the current
reality the gate must express. The Platform's emit-side reads the registry for
consumer roles and gates the add/play affordances, with a discovery-mode empty
state. Buildable now as a self-contained slice (the gate); the playback provider
interface and the Remote Media module are a separate, larger thread. Sequenced
naturally after [platform#23](0023-metadata-as-required-capability.md), so a base
install has metadata (discovery works) before the gate decides what more to show.
