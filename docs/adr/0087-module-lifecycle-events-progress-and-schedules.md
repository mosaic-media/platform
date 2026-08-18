# A module is called for events, in batches, and never calls out

**Status:** Accepted. **Not built.** Supersedes
[platform#7](0007-platform-transports-events.md) **in part** — its ownership
split stands, its assignment of bus interfaces to the SDK does not. Depends on
[platform#85](0085-a-modules-authority-is-declared-and-consented.md) and
[platform#86](0086-a-module-verb-is-declared-and-dispatched-by-name.md). Slice 3
of the extension surface.

## Context

[platform#7](0007-platform-transports-events.md) divided event ownership: the
Platform owns the bus, routing, delivery, reliability and observability; the SDK
owns "the Event Envelope contract, Event Bus interfaces, core Platform lifecycle
events"; modules own their own domain events, namespaced.

**The SDK half was never built and no longer fits.** There is no `Event`, no
`Bus` and no `Subscribe` anywhere in the SDK's non-test surface. #7 was written
when a module was a Go library compiled into the binary, where handing a module a
bus interface to import was reasonable. The extension tier now runs out of
process behind gRPC, where an interface a module imports and calls is a shape
that cannot be satisfied without giving the module a channel out.

Two measurements already exist and settle what would otherwise be guesswork. A
module process is **long-lived**: `Launch` starts it once and the host holds the
client until `Close`, so an invocation is a round trip to a running process, not
a spawn. And `chattiness_test.go` measures the marginal cost of one additional
callback over that boundary, isolating it from spawn and handshake, with a 5ms
ceiling whose own comment says that exceeding it answers
[platform#39](0039-extension-module-boundary.md)'s open question about batched
verbs.

That open question is the one this record closes.

## Decision

**A module is called; it never calls out.** Every lifecycle interaction keeps the
shape a role method already has: the Platform invokes, the module returns, and
the handle it was given stops resolving on return. Nothing in this slice gives a
module a connection, a channel or a callback registration that outlives an
invocation.

**A subscription is declared in the manifest, and events are delivered in
batches.** The module names the event types it wants; the Platform delivers a
bounded batch per invocation, bounded by both a count and a maximum latency
window. This is the amortisation a long-lived module-held stream would have
bought, without the stream: authority is re-established per delivery exactly as
per invocation, and a chatty event costs one round trip per window rather than
one per event.

**Delivery is at-least-once and ordered within a subscriber.** It rides the
outbox the Platform already has, so a module must be idempotent on the event
key — which is the same obligation any outbox consumer already carries.

**Progress is reported through a sink bound to the invocation**, handed in
alongside the `ContentService`
[platform#85](0085-a-modules-authority-is-declared-and-consented.md) already
binds. The module cannot address any other invocation with it, cannot retain it,
and cannot report progress for work it was not asked to do. A verb that reports
no progress passes nothing and closes nothing.

**A module cannot ask a question mid-operation.** Anything needing an answer is a
second verb with declared input under
[platform#86](0086-a-module-verb-is-declared-and-dispatched-by-name.md): the
first returns candidates, the Platform renders them as it renders everything
else, and the second takes the choice. A prompt would make an invocation block on
a human, which an invocation-scoped handle cannot survive, and would make a
module the author of a screen — which is
[platform#21](0021-server-owned-app-shell.md)'s to own.

**A module declares a schedule in its manifest, and a scheduled run acts as the
system principal on the module's consented grants.** It reuses the Runner and
Scheduler in `internal/platform/jobs` and the authority
[platform#85](0085-a-modules-authority-is-declared-and-consented.md) defines.
**The limit is stated rather than papered over: a scheduled run has no user, so a
per-person scheduled verb is not expressible.** "Sync my library nightly" is not
this slice. Making it expressible means deciding what a per-user schedule costs
when it fails for one person and not another, and that is its own decision.

**A module never declares a dependency on another module.** It declares what it
**satisfies** in the Platform, or what it **needs from** the Platform — and the
Platform is always the counterparty, even where another module is what actually
satisfies the need underneath. A module saying "I need streams" is naming a
Platform capability, not a stream provider. It cannot name, discover, or be
broken by another module's release, and the Platform stays free to satisfy the
need however it likes, or to report honestly that it cannot.

## Alternatives considered

**Building #7 as written.** Rejected: it puts a bus interface in a published
contract that a module would reach across a process boundary, which is the shape
[sdk#10](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0010-the-sdk-carries-no-implementation.md)
exists to refuse.

**Superseding #7 wholly.** Rejected because its ownership split is still correct
and still worth having stated. Replacing it entirely would restate the right half
and make a reader diff two records to find what changed.

**A long-lived stream the module holds, with the Platform pushing into it.**
Cheapest per event and it mirrors the session transport. Rejected because it
gives a module something that outlives an invocation, which is what the `Caller`
handle's whole design refuses — and because the cost it saves is a sub-5ms round
trip that batching already amortises without the connection.

**Unbatched delivery, one invocation per event.** Simplest, and within the
measured budget for anything but a chatty event. Rejected as the default because
the batch is a strictly wider shape: a batch of one is the unbatched case, so
choosing unbatched now would mean revisiting the protocol the first time an event
turned out to be chatty.

**Mid-operation prompts.** Rejected above; the cost was a blocking invocation and
a module authoring a screen.

## Consequences

- **A per-user schedule has no path.** A module whose value is per-person
  periodic work cannot express it, and that is a named gap rather than an
  oversight.
- **Modules must be idempotent on event delivery**, because delivery is
  at-least-once. A module that assumes exactly-once will double-apply after a
  retry, and nothing will report it.
- **A batch window is a latency floor.** An event a module reacts to visibly will
  be seen up to one window late, so the window is a user-facing number rather
  than a tuning constant.
- **`chattiness_test.go`'s ceiling now guards a second protocol.** Batched
  delivery should be measured the same way, or the guard covers callbacks and
  silently not the thing this record added.
- **"What a module needs" becomes a Platform vocabulary.** Needing streams,
  metadata or artwork has to be nameable, which makes the set of nameable needs a
  closed set somebody maintains — and a need nobody can express is a finding
  rather than a module's problem.
