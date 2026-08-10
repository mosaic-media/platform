# The correlation ID is the trace ID

**Status:** Accepted (built) — the Shell mints the trace where the user clicks, a
Connect interceptor carries `traceparent` on every call, and `Event.CorrelationID`
has a source. Implementation corrected one thing: the span tree roots at the
caller's span rather than at a phantom the server invented.
**Date:** 2026-07-22

## Context

`domain.Event` has carried a `CorrelationID` field since the event envelope
landed, with a comment that says exactly what its state is:

> `CorrelationID` ties the event to a request or job (envelope:
> `correlation_id`). **Empty until request-scoped propagation exists.**

It is still empty. So is `CausationID`. Nothing in the Platform mints a
request-scoped identifier, which means an `event_outbox` row cannot be tied to
the request that produced it, a log line cannot be tied to either, and a failure
is reconstructed by reading timestamps in three places and hoping.

That was tolerable when the interesting work happened in one process. It is not
tolerable now. A single user action crosses, at minimum:

- the **Shell** (`mosaic-web`, React) — a click,
- the **session transport** ([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md))
  — a unary `Invoke` on one lane, a `RegionUpdate` pushed back on the other,
- **application services** — the command handler order,
- a **module** in its own repository — `module-stremio-addons`,
  `module-remote-playback`,
- **third-party HTTP** — an addon, an aggregator, a debrid provider,
- **PostgreSQL** — the write, and the outbox row committed with it,
- the **outbox worker** — publishing that row on a later tick, on another
  goroutine, possibly minutes afterwards.

Seven hops, four repositories, two processes today and more if modules ever move
out of process. Nothing joins them.

There is also a live example of the cost. The playback decision logs
`playback: chose %q of %d candidates …` from `internal/transport/session/dispatch.go`
— a genuinely useful line — and there is no way to connect it to the `Attach`
that declared the client profile, the resolution that produced the candidates, or
the origin fetch that followed.

## Decision

**Adopt W3C Trace Context. The trace id *is* the correlation id; Mosaic mints no
second identifier.**

- **Every edge accepts a `traceparent` and mints one when absent.** The session
  Connect interceptor, the GraphQL handler, the artwork and playback origins and
  the Supervisor handoff all parse an inbound `traceparent`, continue that trace
  if it is well-formed, and start a fresh one otherwise.
- **`Event.CorrelationID` is the 16-byte trace id, hex-encoded.
  `Event.CausationID` is the span id that published it.** This closes the comment
  quoted above with no schema change: the columns exist, the envelope exists, and
  the only thing that was missing was a source of values.
- **The Shell mints the trace.** A first-party client is where a user action
  begins, so a trace that starts at the server has already lost the interesting
  half. The Shell generates a `traceparent` per intent and sends it as a Connect
  header. **It does not ship the OpenTelemetry JS SDK** — this is a random id, a
  version prefix and a string join, and a media client should not carry a
  general-purpose tracing runtime to produce one.
- **The outbox worker *links*, it does not parent.** An event is published on a
  later tick, after the producing request is finished; making the publish a child
  span of a request that has already returned produces traces that appear to hang
  for minutes. The worker starts its own trace for the drain and attaches a span
  **link** to the trace recorded on the row. Cause is preserved, duration stays
  honest.
- **The id survives sampling.** A sampling decision governs whether *spans are
  recorded*, never whether a trace id exists. Every log line and every event row
  carries the id even when the trace itself was not sampled, so a support report
  is always joinable to the logs even if its spans were dropped.
- **The id is surfaced to the user, deliberately.** An error toast and the
  expert-mode surface ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md))
  both show the trace id, so a bug report arrives with the one string that makes
  it reconstructible.

## Alternatives considered

**Mint a distinct correlation id alongside trace ids.** *Rejected*, and it is
the option the field name invites. Two identifiers means two things to
propagate, two to store, two to index and exactly one that will be forgotten at
some new edge. There is no case where they would legitimately differ.

**Reuse the session id as the correlation id.** *Rejected.* A session spans
thousands of requests over hours; correlating by it groups everything a user did
all evening, which is not a correlation. It is also a *credential-adjacent*
value, and putting it in every log line and every event row spreads it across
surfaces that should never hold it.

**Let PostgreSQL generate the id (a sequence, or the transaction id).**
*Rejected.* The id is needed at the edge — before authentication, before any
transaction opens, and on paths that never open one at all. A database-assigned
id also cannot be sent to a module or returned to a client at the moment the work
begins.

**Propagate a bespoke header rather than W3C Trace Context.** *Rejected.* There
is a standard, every OTel-compatible tool understands it, gRPC and HTTP
instrumentation implement it for free, and inventing a header buys nothing but a
translation layer at every boundary and at any future OTLP export.

## Consequences

- **The outbox becomes joinable.** An `event_outbox` row, a log line, a span and
  an audit record ([platform#35](0035-audit-is-a-store-not-a-log-stream.md)) share
  one key. This is the specific thing that makes a multi-repository bug
  tractable, and it is why this record ranks above tracing itself.
- **A bug report becomes a lookup.** "It failed" plus a trace id is a query, not
  an investigation.
- **Modules get propagation without adopting anything.** Trace context lives in
  `context.Context`, which a capability already receives on `Import`. A module
  that never mentions telemetry still has its work attributed correctly, because
  the seam around the invocation
  ([platform#33](0033-instrument-at-the-seams.md)) is what records it.
- **An inbound `traceparent` is untrusted input.** It is parsed strictly and
  discarded if malformed, and it is never used for anything but correlation —
  never for authorisation, routing or rate limiting. A client controls it, so a
  client can forge it, and it must never be load-bearing.
- **Trace ids are not secrets but they are linkable.** They tie a user's actions
  together across surfaces by design, which is exactly why the expert-mode viewer
  is permission-gated
  ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md)).

## Implementation implications

A `traceparent` parse/format pair and the propagator wiring in
`internal/platform/telemetry`, seeded at each edge by
[platform#33](0033-instrument-at-the-seams.md)'s seams. `Service.newEvent` stamps
`CorrelationID` and `CausationID` from the context instead of leaving them empty
— a two-line change that has been waiting for this record. The Shell gains a
small `traceparent` minter and sends the header on every unary intent; the
session interceptor reads it. `internal/transport/session/dispatch.go`'s playback
decision line becomes a structured, trace-stamped event in the same slice, since
it is the worked example.
