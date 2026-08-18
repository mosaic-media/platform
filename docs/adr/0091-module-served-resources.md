# A module's bulk output is served from the Platform's origin, signed and relayed

**Status:** Accepted. **Not built.** Depends on
[platform#90](0090-one-origin-facility-consumers-declare-against.md) and
[platform#85](0085-a-modules-authority-is-declared-and-consented.md). Slice 6 of
the extension surface.

## Context

A module can return metadata, streams and artwork references. It has nowhere to
put bulk output: manga pages, lyrics, EPUB content, a read-along sync map. These
all want the same thing — a URL a client can fetch, for bytes a module produced.

One answer is already foreclosed.
[platform#25](0025-playback-consumer-and-media-origin.md) settled that a module
never speaks HTTP, so a module cannot serve its own bytes. The Platform is the
origin whatever else is decided.

## Decision

**Module resources are the third consumer of the origin facility**
([platform#90](0090-one-origin-facility-consumers-declare-against.md)), declared
rather than built again.

**Signed, not sealed.** A module resource reference is Platform-side identity —
the module, the node, the resource kind and a key — and none of it is a secret.
Signing keeps the URL cacheable and legible in a trace, which is the artwork
proxy's reasoning and it applies here unchanged. A module whose resource
reference would itself be sensitive must keep the secret out of the reference
rather than reach for sealing.

**The Platform asks the module and relays.** The module answers a resource
request over the same call-in path as everything else, and the Platform serves the
bytes from its own origin.

**When module storage lands, serving from storage is an optimisation behind the
identical URL.** A stored resource is served from disk and a dynamic one is
fetched from the module, and a client cannot tell which happened. This is why
slice 6 does not wait for slice 7: the URL shape is the contract, and where the
bytes come from is not part of it.

**The Platform caches only what the module declares immutable.** A manga page for
a given release never changes; tonight's generated EPG does. The module is the
only party that knows which, and an undeclared resource is treated as mutable, so
the safe answer is what happens when nobody thought about it.

**A resource URL carries no authority beyond fetching that one resource.** It
cannot be exchanged for a session, cannot widen into a listing, and a module
cannot mint one — only the Platform can, on a request it authorised, bound to the
session id under
[platform#90](0090-one-origin-facility-consumers-declare-against.md). This is
[platform#88](0088-a-contribution-composes-from-published-definitions.md)'s
principle again: no mechanism may offer a route around the permission system.

## Alternatives considered

**Waiting for module storage and serving only from disk.** Fast and cacheable
from the first day, and it survives the module being down. Rejected because it
reorders the milestone and leaves a genuinely dynamic resource with no route at
all.

**Sealing the reference, as playback does.** Nothing leaks, ever. Rejected: it
treats every module resource as a credential when almost none are, and it costs
caching and legibility for a threat that is not present.

**Letting the module serve its own bytes.** Rejected by
[platform#25](0025-playback-consumer-and-media-origin.md), and worth restating
because it is the obvious thing to reach for when relaying looks expensive.

## Consequences

- **Until storage lands, a chapter is a round trip per page and the module must
  be running.** That is a real cost of landing this slice first, accepted so the
  URL shape can be fixed before anything depends on it.
- **A module that declares immutability wrongly serves stale bytes**, and nothing
  will catch it. The declaration is trusted because only the module can know, and
  that trust has no verification behind it.
- **Bulk output crosses the process boundary twice** — module to Platform, then
  Platform to client. For a sync map of ~100k triples that is worth measuring
  against the chattiness ceiling before the first such module ships.
