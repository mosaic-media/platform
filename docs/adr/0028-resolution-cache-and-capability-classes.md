# The resolution cache and capability classes

**Status:** Accepted (built, except the refresh job). **Invalidate-on-read
landed**: the ticket carries the part and the capability class, and the origin
re-resolves once and retries on an upstream failure — on the relayed path, the
pipe path and the segment path, each before a byte reaches the client. A source
that returns the same address is treated as no answer rather than as grounds for
an identical retry.
Capability classes are derived on `Attach` from the declared profile
([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)) and the `playback_resolutions` store is keyed by (part, capability
class), read after selection and written after resolution — verified against
real PostgreSQL. **The background refresh job is still unbuilt, but no longer
blocked**: the jobs runner, the interval scheduler and the system principal all
landed in M0.1 (`internal/platform/jobs`, `Service.SystemCaller`), and four
handlers are registered on them. The resolution-cache refresh is simply not one
of them — it is queued work now rather than a prerequisite. Implementation departed in two
places: a cache hit reports no module id, because no module was asked, and the
write runs on the request goroutine rather than in the background — which still
honours the "must not block the stream" requirement, since nothing is streaming
when it runs.
**Date:** 2026-07-22

## Context

[platform#27](0027-stream-selection-against-a-client-profile.md) has the Platform
ask a source for candidates at play time and rank them against the calling
client's profile. That is correct and it is slow in the one place a user
notices. Measured against what the work actually costs, the ranking is free, the
ticket is free, and **the aggregator call dominates everything else** — an
addon like AIOStreams fans out to many scrapers, and the answer arrives in
hundreds of milliseconds to several seconds. Doing that between a click and a
first frame is the whole latency budget spent on one call.

The obvious fix is to keep the resolved URL. What makes it non-obvious is that a
debrid link is **perishable in a way that has no contract**. Lifetimes vary by
provider and are commonly on the order of days, but a link also dies the moment
its torrent falls out of the provider's cache, whatever its age. So a stored URL
is a cache with no trustworthy expiry — which means the design cannot be built on
a TTL, and must be built on detecting failure.

There is a second thing the naive version gets wrong. Storing *every* candidate's
URL means holding twenty-odd perishable things per item to use one of them, and
refreshing twenty-odd to keep one warm. Worse, resolving them at import is the
worst possible moment: import is when a URL has the longest time to go stale
before anyone plays it. A library added in July and watched in August has a
hundred percent miss rate.

Two properties of Mosaic decide how this is keyed, and getting them wrong
personalises something that is not personal:

- **The library is install-global** ([platform#9](0009-object-graph.md)). Content,
  its tree and its candidate releases are shared by every user; nothing about a
  release belongs to a person.
- **A resolved URL is a property of the bytes and the screen**, not the viewer.
  Two people watching on identical televisions want the identical URL.

## Decision

**Separate the durable candidate from the perishable link. Persist every
candidate as a Part at import; resolve URLs lazily, keyed by the capability
class that needed them; invalidate on failure rather than on a clock.**

- **Candidates persist as Parts, without URLs.** At import the source's full
  candidate set — release name, quality, container, codec, size, swarm health,
  and the magnet or infohash as the stable location — is written as Parts of the
  item. `Part` already carries `Container`, `VideoCodec`, `AudioCodec`, `Width`,
  `Height`, `HDRFormat`, `BitrateBPS` and `SizeBytes`; those fields exist so that
  something can choose between several Parts of one item
  ([platform#10](0010-storage-authority-and-transaction-scope.md)'s single
  source-selection path), and this is that use. Candidates never expire, so
  storing all of them costs nothing to keep correct.
- **Probe results join the durable tier.** What a release actually *is* —
  container, video codec, every audio track — describes the bytes rather than the
  viewer or the moment, so it persists on the Part beside the candidate and is
  never re-derived ([platform#29](0029-probing-and-the-per-stream-playback-decision.md)).
  A re-resolved URL for the same release does not re-probe: the link changed, the
  file did not.
- **A capability class, not a device and not a user.** A client declares its
  profile on `Attach` ([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)); the
  Platform reduces that declaration to a **class** — a stable digest of the
  containers and codecs it can decode. Five phones with identical support are one
  class. The resolution cache is keyed by **(part, capability class)**. A user
  does not appear in the key at all, which is the correction this record makes to
  an earlier sketch: personalising a resolved URL would have produced one row per
  person for a value none of them own.
- **Resolution is lazy, never eager at import.** The URL column starts empty and
  fills in the first time a class actually asks. Resolving at import buys a cache
  entry that has been decaying since before anyone wanted it.
- **Invalidate on read, and retry inside the origin.** A play uses the cached URL
  directly, with no liveness pre-check — a pre-check spends a round trip on every
  play to catch a failure that is rare. When the fetch fails, the **origin**
  ([platform#25](0025-playback-consumer-and-media-origin.md)) re-resolves from the
  source, updates the cache and continues. It can do this invisibly because it
  fetches upstream *before* writing anything to the client, so nothing has been
  committed to the response yet. The happy path costs nothing; the miss costs
  what the pre-check would have cost anyway; and the client never sees a failure
  Mosaic can fix itself.
- **The cache write must not block the stream.** Resolve, start serving, persist
  after. A user waiting on a database write to watch a film is the wrong
  trade in the one place latency was the whole point.
- **Background refresh is scoped to what someone is actually watching.** A cron
  refreshes only items with playback progress
  ([platform#26](0026-playback-state-is-platform-owned.md)) — plus the next episode
  of an in-progress series, which is where a stall is least tolerable. The
  library at large is never refreshed: most of it will not be played soon, and
  refreshing it would re-introduce exactly the cost this record removes.
- **A stale entry is corrected, not deleted.** A failed resolution overwrites
  with the new URL; the Part it belongs to is untouched, because the *candidate*
  was never wrong.

## Alternatives considered

**Store one URL per item — whichever release import picked.** *Rejected.* It
cannot serve two device classes, so a phone and a 4K television share one answer
and one of them is wrong. It is also what the code does today, and it is why the
library is only playable shortly after import.

**Store a URL for every candidate.** *Rejected.* Twenty-odd perishable rows per
item to use one, and twenty-odd to refresh to keep one warm. The candidate set is
worth persisting; the URL set is not.

**Resolve every capability class eagerly at import.** *Rejected.* Import is the
moment furthest from use, so eager entries have the highest chance of being dead
on arrival, and the cost is paid for classes that may never request that item.

**Trust a TTL and refresh before expiry.** *Rejected as the primary mechanism.*
Provider lifetimes are neither uniform nor guaranteed, and cache eviction kills a
link early regardless of age. A TTL is a useful hint for how eagerly to refresh;
it is not something correctness may rest on.

**Check liveness before playing.** *Rejected.* It converts a rare failure into a
round trip on every single play — precisely the latency this record exists to
remove — and the origin can recover transparently anyway.

**Key the cache by user and device.** *Rejected*, and it is the sketch this
record corrects. The library is shared and a URL is a property of the bytes and
the screen; keying by user duplicates an identical value per person, and keying
by device duplicates it per identical device.

## Consequences

- **A warm play skips the aggregator entirely** — read the Part, read the cached
  URL, mint, fetch. That is about as close to one round trip after the click as
  this can be, and the addon is called only on a genuine miss or a dead link.
- **A new capability class is a cold cache for the whole library.** The first
  play on a newly added television pays the full round trip, every time, until
  the cron has warmed its in-progress items. That is correct behaviour, and it
  will *look* like a regression on a new device unless it is expected.
- **The refresh job is blocked, and the read path is not.** Invalidate-on-read
  works with what exists today. The cron needs a scheduler, the jobs runner (the
  `jobs` tables exist with no service) and — since background work has no session
  — the **system principal** [platform#13](0013-how-a-capability-acts.md) reserved.
  Those are one slice, and the read path must not wait on them.
- **Import gets more expensive and more useful.** Writing every candidate is more
  rows than one, and it is what makes a source picker and per-class selection
  possible without going back to the source.
- **One debrid account serves the install.** Module settings are per-module, not
  per-user ([platform#17](0017-module-settings.md)), so the cached URL is
  install-wide. That is harmless today because the Platform relays and the
  credential never reaches a client, but it does mean one account's entitlement
  is shared by every user. Per-user provider credentials are a real question this
  record does not answer.
- **Capability classes need a stable digest.** Two clients that describe the same
  support in a different order must land in the same class, or the cache
  fragments silently and the only symptom is that it never seems warm.

## Implementation implications

A Platform-owned store keyed by (part id, capability class) holding the resolved
URL, any headers, and when it was resolved. The class digest is computed where
`Attach` receives the declared profile
([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)) and carried on the session. The
Stremio module's import path grows from attaching one stream to attaching the
whole candidate set as Parts. `ResolvePlayback`
([platform#25](0025-playback-consumer-and-media-origin.md)) reads the cache before
it reaches for a provider, and the origin gains the re-resolve-and-retry path.
The refresh job is deliberately not in this slice.
