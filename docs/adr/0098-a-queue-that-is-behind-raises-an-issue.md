# A queue that is behind raises an Issue, and the batch numbers are the outbox's own

**Status:** Accepted. **Not built.** Fixes the two numbers
[platform#87](0087-module-lifecycle-events-progress-and-schedules.md) left open,
and uses [platform#74](0074-operational-findings-are-durable-state.md)'s register,
which is built. Closes the last item on
[architecture](https://github.com/mosaic-media/architecture/blob/main/docs/index.md)'s
deliberately-undecided list.

## Context

The entry was one line — "backpressure thresholds and queue bounds" — and reading
the code turns it into a specific statement: **every queue here is bounded on the
drain side and unbounded on the fill side.**

Draining is well covered. The outbox worker takes fifty events per poll on a
one-second interval; a failed delivery gets exponential backoff and dead-letters
after eight attempts; a single event's failure does not stop the batch. The job
runner claims four at a time on a two-second interval, dead-letters after five,
and drains again immediately when a claim came back full so ten queued jobs do
not take ten intervals.

Enqueueing is covered by nothing. There is no depth at which anything refuses,
warns, or behaves differently. A queue growing faster than it drains — a source
gone slow, a wedged module, an import of a large library — grows until PostgreSQL
runs out of disk, and the first symptom is every write in the Platform failing
rather than anything naming the backlog that caused it.

The in-memory buffers are the well-behaved ones, and they are worth reading
before deciding anything about the durable ones. The telemetry sink is a bounded
channel that never blocks and never fails: when full it discards the *oldest*
record and counts the loss, on the reasoning that under sustained pressure the
recent records are the interesting ones and dropping new arrivals would preserve
a stale prefix of the incident while losing the incident. The session mailbox
retains 256 messages and rebuilds a client from scratch when its cursor falls
before the window. Both are bounded, both state which end they discard from, and
both make the loss visible.

Those are not a template for the durable queues, and the difference is the point:
a telemetry record is an observation, and losing one degrades a view. An outbox
event is work somebody is waiting on, and losing one is a lost effect.

platform#87 also left two numbers open on purpose — a module's event batch is
bounded by a count *and* a maximum latency window, neither chosen. The measured
constraint is that a callback across the module boundary costs about 5ms, guarded
by a standing ceiling, so batching is worth roughly its own round trip.

## Decision

**A durable queue does not refuse an enqueue. Depth raises an Issue.**

A backlog is not an error in whoever enqueued — it is a fact about the
deployment, and platform#74's register is exactly the shape for one: a durable,
typed statement that something is operationally wrong, held until resolved, with
actions offered against it. Its identity is type, context and reference, and
`FirstSeen` survives a re-raise, so a queue that has been behind since Tuesday is
one row saying so rather than one row per poll.

Refusing instead would put the failure on whoever acts next, who is almost never
whoever caused the backlog: a person pressing Play is told the system is
unavailable because an unrelated import is behind. That is a worse answer than
carrying on and telling somebody.

**The threshold is age, not depth.** A row count means nothing without a drain
rate — ten thousand outbox rows is three minutes behind at fifty a second, and
would be three hours behind at one. The measure that is directly meaningful, and
comparable across queues that drain at different rates, is how old the oldest
undelivered item is. It is also the measure that keeps being right when a batch
size changes.

**A new `IssueType` is added for it**, which is growing a closed vocabulary and
therefore a decision rather than an edit — the set is closed because Platform code
branches on it to choose suggestions and a client branches on it to choose words.
It carries `dismiss` and nothing else, because there is no automatic remedy: what
to do about a backlog is to find out what stopped draining, and a build cannot do
that on a person's behalf. Offering a fake action would be worse than offering
none.

**The in-memory buffers are unchanged.** Their policy is already right and this
record does not disturb it.

**platform#87's two numbers are the outbox's own: fifty per batch, and a
one-second window.** Module delivery rides the outbox, so reusing them means a
module's batch *is* the outbox's drain rather than a second pair of numbers that
can drift out of step with the first. At 5ms a round trip, fifty events amortise
to a tenth of a millisecond each, which is the amortisation a module-held stream
would have bought.

## Alternatives considered

**A hard depth ceiling, with enqueue failing past it.** *Rejected:* it bounds the
disk and it misdirects the failure, as above. It also converts a slow subsystem
into a Platform-wide outage, which is a larger blast radius than the problem.

**Shed by class — refuse background work, always accept user-initiated work.**
*Rejected:* attractive, and it makes every enqueue site declare a class. A class
is exactly the kind of field that gets defaulted wrong once and is then never
noticed, and the wrong default here silently discards work.

**Depth rather than age as the threshold.** *Rejected:* a count that means
different things in different queues, and that stops meaning what it did the day
a batch size changes.

**A per-subscription window declared in the manifest.** *Rejected:* it lets a
module choose the Platform's cost, and every module would ask for the shortest
window because nothing charges it for that.

**An adaptive window that widens as the backlog grows.** *Rejected:* delivery
latency becomes a function of load, so a module's behaviour is not reproducible
and no test can pin it.

## Consequences

**A backlog can still fill a disk.** This trades a hard stop for a warning, and a
warning depends on somebody reading it. That is the accepted cost, and it is
softened by the register being a screen rather than a log line — but not removed.

**Nothing measures queue age today.** The threshold needs a probe that does, which
is the same gap platform#92 named for per-module resource use: a decision that
rests on a measurement nobody takes is not implemented by writing the decision
down.

**One second becomes a visible latency floor** for anything a module reacts to on
screen, and because the number is now shared, tuning it for module delivery tunes
the outbox. That coupling is deliberate — one number to reason about — and it is
the thing to revisit first if either side turns out to want a different answer.

The threshold value itself is configuration and declares a reload class like every
other field. What this record fixes is the *measure* and what crossing it does,
not a number that will differ between a laptop and a household server.
