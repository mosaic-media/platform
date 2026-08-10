# Playback state is Platform-owned

**Status:** Accepted (built, except the continue-watching rail and watched marks
on `EpisodeRow`, which are slice 6). The store, the commands and queries, the
session-side coalescing and the Resume affordance are built and verified live.
Implementation added a floor this record does not anticipate — a completion
threshold cannot be applied to a duration a player has not settled on, or an
item finishes the instant it starts — and a `Found` flag distinguishing "never
started" from "started and at zero", which a detail screen renders as Play
versus Restart. Building it also surfaced something outside its scope: a preset
is snapshotted into a role row at creation, so `playback.write` never reached an
account seeded before the action existed; the bootstrap now reconciles the owner
account's role on every boot. The multi-user visibility question this record
opens and does not answer is answered by
[platform#59](0059-one-library-many-viewers.md): the library is shared, the
continue-watching rail is not.
**Date:** 2026-07-22

## Context

Resolving bytes makes a library playable ([platform#25](0025-playback-consumer-and-media-origin.md)).
It does not make it *usable*. The difference is entirely position state: where a
user stopped, what they have finished, and what they should be offered next. A
media server without resume is a file browser with posters.

Stremio is again the useful reference, and again for its split rather than its
features. Its addons know nothing about progress; its player reports a
`timeOffset` and a `duration` against a library item, and that state syncs to an
account, independent of which addon sourced the stream. Watching a film through
one addon and resuming it through another is the same item at the same offset.
That independence is the design, not an implementation detail — position belongs
to the *content and the viewer*, not to the source that happened to serve it.

Mosaic has nowhere to put it. The object graph holds nodes, parts, relations and
source bindings ([platform#9](0009-object-graph.md)); none of them are per-user, and
there is no store, service or query for viewing state anywhere in the Platform.
This is what makes the home screen a static rail of Popular rather than a place a
user returns to.

The tempting shortcut is to let the playback module keep it — it is the thing that
knows the offset, and it is already handling the session. That collides directly
with [platform#8](0008-capabilities-do-not-own-stores.md): **capabilities own no
schema.** It also collides with the product: state written by the playback module
would be lost when a user switches playback modules, invisible to a local-file
player, and unavailable to any module that has no business resolving bytes but
every business reading progress (export, request, recommendation).

## Decision

**Playback state is a first-class, Platform-owned, per-user concern, written
through the published content surface and readable by any consumer. No module
owns it.**

- **A Platform store**, alongside the four content stores on `Tx` — growing the
  Platform's store set is deliberate Platform evolution and should look like it.
  State is keyed by **(user, node)**, not by part: a user resumes *an episode*,
  not *the 1080p Torrentio release of an episode*. Which Part served the bytes is
  a property of a session, not of the position, exactly as Stremio's split has it.
- **The state is small and closed**: position, duration, a finished marker, when
  it was last touched, and the release last played. It is a closed vocabulary
  ([platform#11](0011-open-and-closed-vocabularies.md)) — unlike media types, there
  is no per-format variation to absorb, and an open document here would be a
  place for modules to smuggle schema.
- **Position is device-independent; the release is not.** The state records the
  Part last played so a resume returns to the *same release*, which matters more
  than it sounds: two encodes of one film differ by however much their intros
  differ, so resuming a different release lands the viewer at the wrong moment.
  When the device asking is a different one and cannot play that release,
  selection falls back to what it can ([platform#27](0027-stream-selection-against-a-client-profile.md))
  and the small drift is accepted knowingly — the alternative is making a phone
  stream a 4K HDR remux to keep a timestamp exact.
- **This is the only per-user tier.** Content, its tree and its candidate
  releases are install-global, and a resolved URL belongs to the bytes and the
  screen rather than the viewer ([platform#28](0028-resolution-cache-and-capability-classes.md)).
  Position is the one thing that is genuinely personal, which is what makes
  (user, node) the right key here and the wrong key anywhere else.
- **Written through `ContentService`**, as commands with the same handler order
  as every other write: validate, authenticate, authorise, transact state and an
  outbox event together. A consumer module records progress **as its invoking
  user** ([platform#13](0013-how-a-capability-acts.md)), so a user can never write
  another user's position.
- **Reads are queries on the same surface** — one node's state, and an
  in-progress list ordered by recency. The in-progress list is what the
  continue-watching rail renders; it is a query rather than a client-side fold so
  that every client gets it identically ([contracts#1](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0001-server-driven-ui-and-the-shell.md)).
- **Progress is reported by the client and coalesced by the Platform.** The
  player emits position intents on a slow cadence and at meaningful boundaries
  (pause, seek settled, exit); the Platform coalesces them the same way it already
  coalesces input ([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)) so a
  playing video does not become a write per second.
- **Finished is derived, then sticky.** Crossing a completion threshold near the
  end marks the item finished; an explicit user mark overrides it in either
  direction and is not re-derived. Deriving alone gets credits wrong; manual
  alone is a chore.

## Alternatives considered

**The playback module owns progress.** *Rejected.* It violates
[platform#8](0008-capabilities-do-not-own-stores.md) outright, and it produces the
wrong product: state that dies with the module, is invisible to a second player,
and cannot be read by any non-playback consumer.

**Keyed by Part rather than node.** *Rejected.* A user who watches half an episode
from one source and resumes from another has one position, not two. Part-keyed
state would present that as two half-watched copies of the same episode, which is
the bug Stremio's item-level state avoids.

**Store it in `Node.Attributes`.** *Rejected.* Attributes are per-node and
unvalidated ([platform#9](0009-object-graph.md)); progress is per-*user*-per-node.
Encoding a user dimension inside a shared node's attribute document is a
concurrency hazard and a privacy leak between users of one install.

**Defer it until after the player ships.** *Rejected.* Resume is not a polish
item on top of playback — it is the first thing missing the moment playback works,
and the player's contract has to carry a resume offset from the start
([web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)). Building the player against a
state surface that does not exist means building its contract twice.

**A watched *bitfield* per series, as Stremio keeps.** *Rejected as premature.* It
is a compression of exactly the per-episode rows this store already holds, and it
exists in Stremio because its state rides a synced document rather than a
database. Mosaic has a database.

## Consequences

- **Mosaic becomes a place a user returns to.** Continue-watching on home,
  resume on the detail screen, watched marks on episode rows — all of it is this
  one query, rendered server-side.
- **The store set grows for the first time since the content model.** Four content
  stores became five. That is the deliberate-evolution rule being exercised, not
  bent.
- **The relation-read gap becomes load-bearing.** "Next episode" and
  series-level rollup ("3 of 12 watched") need to walk the graph, and
  `ContentService` still cannot read edges — the gap open since the reference
  capability. It is now on the critical path of a user-visible feature rather
  than a tidiness item.
- **A second consumer gets it free.** A future local-file player, an export
  module writing NFO `<watched>`, and a request module deciding what to acquire
  next all read the same surface. This is the payoff for refusing to let the
  playback module own it.
- **Per-user rows arrive in the content domain.** Everything in the object graph
  so far is install-global; this is the first content state that differs between
  two users of one Mosaic. Multi-user visibility rules (does a household share a
  continue-watching rail?) are a real question this opens and does not answer.

## Implementation implications

A Platform store and migration; the commands and queries on `ContentService` (SDK,
same version bump as [platform#25](0025-playback-consumer-and-media-origin.md)'s
consumer surface); coalescing on the session, beside the existing input
coalescing; and emit-side work — a continue-watching section on home, a Resume
affordance on detail, watched state on `EpisodeRow`. The rail is gated like every
other library affordance ([platform#24](0024-capability-gated-affordances.md)): with
no consumer installed there is nothing in progress and nothing to show.
