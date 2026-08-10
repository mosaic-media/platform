# Cache-first rendering, and telling the truth when a source is down

**Status:** Accepted and built (roadmap M2.6). **Built in part in one respect:**
only home and its catalogs render cache-first, as this record scopes them; a
drill-down that pages or narrows a catalog still asks live. The pending
indicator this record expected a stale-while-revalidating screen to reuse was
not reused — the client's flag is driven by its own in-flight intents and a
server-scheduled revalidation is not one — so a screen served because
revalidation failed states its age in the standing notice and in a banner
instead.
**Date:** 2026-07-22

## Context

Restarting the Platform under a live client produced a home screen reading
**"Nothing to show yet — try adding an addon in Settings"** — on an install with
an addon configured and a library full of content.

The client behaved correctly throughout, which is worth stating because it was
initially blamed: the stream ended, it reconnected without being told to, and
the server rebuilt and pushed a fresh screen. Nothing needed refreshing. What
was wrong was *what got pushed*.

The emit-side fans out to every catalog and keeps what succeeds:

```go
items, err := s.content.ListCatalogItems(...)
if err == nil {
    itemsByCatalog[i] = items
}
```

An error is discarded — not logged, not counted, not surfaced. When every
catalog fails, `len(rows) == 0` and the screen renders the empty state. In the
seconds after a restart, when addon manifests have not been fetched and the
first calls are cold, that is exactly what happens.

So a transient upstream failure is presented as a **configuration mistake**,
which is worse than an error: it sends a user to fix something that is not
broken. It is also very likely why refreshing appeared to help — a reload
retried the fetch, the addons were warm by then, and content appeared. The
refresh was not repairing the transport; it was retrying a failed read.

Underneath sits a structural point. Every source-backed screen is rendered by
calling an aggregator live, and that call takes seconds when it works
(measured: 9.6s cold, 2.6s warm for one search). The screen has no other way to
be right, so a slow or absent source means a slow or wrong screen, every time.

## Decision

**Render source-backed screens from a durable snapshot when one exists, revalidate
in the background, and push the live result when it arrives. Never present a
source failure as an empty library, and say so persistently when a source stays
unreachable.**

- **This is stale-while-revalidate, not "optimistic UI".** The distinction is
  worth keeping: optimistic rendering shows a predicted *write* outcome before
  the server confirms it. Nothing here predicts anything — it serves the last
  known-good *read* and replaces it with a fresh one. The controlled vocabulary
  in `docs/index.md` exists because one word meaning several things has already
  cost this project real work.
- **Snapshot the data, never the rendered screen.** A cached `UINode` tree would
  be faster and would break invisibly: artwork URLs are signed with a
  **process-scoped key**, and playback tickets sealed with another, both
  regenerated on boot. A tree cached before a restart comes back full of URLs
  signed by a key that no longer exists — images fail and the page *looks*
  right. The snapshot holds catalog items (ref, title, year, source poster URL)
  and the screen re-renders from them, signing with the current key.
- **Durable, in the Platform's own storage.** The point is surviving a process
  restart, which an in-memory cache cannot. It also means an addon being down
  for an hour is survivable, not just a reboot — the more common case.
- **First render with no snapshot waits, and that is correct.** A cold install
  has nothing to show and should say it is working rather than inventing
  something. It writes a snapshot on the way, so it is the only slow one.
- **Revalidation runs in the requesting session's context.** It has a real
  caller, so it needs no system principal ([platform#13](0013-how-a-capability-acts.md)'s
  reserved gap stays reserved) and every read still authorises as the user who
  caused it.
- **The live result arrives as a `RegionUpdate`** — the first real use of the
  op-set [contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md) defined and
  nothing has exercised. `REPLACE` is the honest starting point; `PATCH` earns
  its place once a revalidation changes one row rather than the page.
- **"No results" and "the source failed" must never render the same.** They are
  different states and only one of them is the user's to act on. A failed fetch
  is logged, counted, and rendered as a failure — never as an empty library.
- **A source that stays unreachable gets a persistent notification.** A toast is
  transient by design ([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)
  carries them for exactly that): right for "import finished", wrong for a
  condition still true a minute later. Degraded-but-working is a state the
  interface should hold, not announce once and forget.
- **One notification surface, two lifetimes — not two surfaces.** A persistent
  notice appears exactly where a toast does and shares its stack: new arrivals
  enter at the bottom and push anything still standing upward. What differs is
  only *when it leaves* — a toast expires on a timer, a notice stays until the
  user dismisses it or the Platform clears it because the condition resolved.
  Giving a lasting condition its own region would put two competing places to
  look for "something is wrong", and the one that appears less often is the one
  people stop checking.
- **A notice is therefore identified, not just displayed.** A toast can be
  fire-and-forget because it removes itself; a notice the server must later
  retract cannot. It carries an id so a repeat failure updates the standing
  notice rather than stacking a fifth copy of it, and so recovery can clear the
  exact one it fixed.
- **Staleness is shown, not hidden.** A snapshot rendered while revalidation is
  in flight reuses the pending indicator; a snapshot being served *because*
  revalidation failed says so, with its age. A two-day-old home screen beats an
  empty one, but only if nobody is being told it is live.

## Alternatives considered

**Keep failing fast and show the empty state.** *Rejected — it is the current
behaviour and it lies.* An install with content and a configured addon was told
it had neither.

**Cache in memory.** *Rejected.* It evaporates on the restart this exists to
survive, and does nothing for a source that is down.

**Cache the rendered `UINode` tree.** *Rejected*, and the reason is not
performance. Process-scoped signing keys mean a cached tree's artwork and
playback URLs are dead on arrival after a restart, producing a page that looks
correct and is broken — the worst failure mode available.

**Retry with a spinner instead of a snapshot.** *Rejected as the primary
answer.* It makes every cold start slow again and offers nothing when the source
is genuinely down. Retry is still what revalidation does; it just is not what
the user waits on.

**Surface failures as toasts only.** *Rejected.* A transient message for a
persistent condition means the state is announced once and then invisible, so a
user who looked away has no way to know the library is stale.

**Snapshot everything.** *Rejected as scope.* Only source-backed screens on the
critical path need it — home and its catalogs first. A snapshot of something
nobody renders is storage and staleness for nothing.

## Consequences

- **A restart stops looking like a broken install.** The screen is populated
  before any addon answers, and the first push after reconnect is content rather
  than an apology.
- **Source outages become survivable rather than fatal.** The library keeps
  working, degraded and honest, instead of appearing to be unconfigured.
- **The `RegionUpdate` op-set is finally exercised.** It has existed since
  [contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md) with nothing using it, which means it is also unproven — expect the
  first revalidation push to find something.
- **Staleness becomes a visible concept.** Once a screen can be old, "how old"
  and "is it refreshing" are questions the interface has to answer, and the
  answers have to stay true.
- **Storage grows with the library.** Bounded by what is actually rendered, and
  small — items, not artwork — but it is a new thing to keep correct.
- **Source health becomes a first-class signal.** Counting failures per source
  to drive a notification is the beginning of something a settings screen will
  want too: which of my addons is actually working.
- **It does not make anything faster.** A cold cache is exactly as slow as
  today, and revalidation still costs a full round trip. What changes is that a
  user no longer waits on it, which is a different and better property than
  speed.

## Implementation implications

A Platform-owned snapshot store keyed by screen and its parameters, holding the
item lists a screen renders from and when they were taken. The home builder
reads the snapshot before it fans out, renders from it when present, and
schedules revalidation; on success it pushes a `RegionUpdate` to the requesting
session. Catalog failures are logged and counted rather than discarded, and the
empty state is split into "nothing configured" and "sources unreachable". A
failure counter per source drives a standing shell-region notification, cleared
when a source next answers.
