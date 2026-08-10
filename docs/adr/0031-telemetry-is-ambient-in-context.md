# Telemetry is ambient in context

**Status:** Accepted (built) — no constructor takes a logger, `telemetry.From(ctx)`
is the call, and two `Printf`s survive in non-test code.
**Date:** 2026-07-22

## Context

The Platform has a good structured logger and does not use it.
`internal/platform/diagnostics.Logger` writes JSON lines with a **fail-closed**
redaction model — a `Field` is replaced with `[REDACTED]` unless a caller
explicitly built it with `String()` — and it is called from three non-test
places: two health snapshots in the composition root and the Supervisor handoff.

Everything that actually reports what the process is doing bypasses it.
`cmd/mosaic-platform/main.go` prints boot progress with `fmt.Printf`. The session
transport reports stream open, stream close, session reaping and the playback
decision with `log.Printf` — unstructured, unredacted, no request scope, straight
to stdout. Those are precisely the lines someone reads when a session misbehaves,
and they are the ones that carry the least.

The reason is not negligence, it is friction. Reaching the logger means holding
one, which means a constructor parameter, which means threading a dependency
through every service, transport and store that might one day want to say
something. Nobody does that for one log line, so they reach for `fmt`.

Meanwhile the thing a log line most needs — *which request is this?* — is not
available to a constructor at all. It is per-call. A logger injected at
construction cannot know the session, the actor, or the trace the call belongs
to, so even the disciplined path produces lines that must be correlated by
timestamp and guesswork.

Both problems have the same shape, and Mosaic already has the carrier for the
solution: **every application service method, every store method, every transport
handler already takes a `context.Context`**, because the command handler order
([CLAUDE.md](https://github.com/mosaic-media/platform/blob/main/CLAUDE.md), and
[platform#8](0008-capabilities-do-not-own-stores.md)'s transaction shape) requires
one.

## Decision

**Telemetry is ambient: it rides `context.Context`, and no constructor anywhere
takes a logger, tracer or meter.**

- **One package, `internal/platform/telemetry`, in the Platform tier.** It
  depends on the standard library and the OpenTelemetry *API* module only —
  never the OTel SDK, which the composition root alone configures. It is a peer
  of `diagnostics`, which keeps health aggregation and the support bundle;
  the redaction vocabulary moves here so both use one
  ([platform#34](0034-redaction-classes-are-the-pii-boundary.md)).
- **`telemetry.Into(ctx, …)` at the edges, `telemetry.From(ctx)` at the point of
  use.** An edge — a transport handler, a job, the composition root — binds what
  it knows once: process identity, trace and span ids, session, actor, component,
  module. Everything downstream calls `telemetry.From(ctx).Info("…", fields…)`
  and inherits all of it without naming any of it. That is the whole ergonomic
  claim: **one line at the call site, full correlation in the output.**
- **`From` on a context that was never seeded returns a working no-op**, not nil
  and not a panic. A logger that can crash the process is worse than no logger,
  and a test or a library path that forgot to seed must degrade quietly.
- **`domain` never imports it.** Dependency direction is unchanged: the domain
  applies rules and returns results; it does not narrate. Application services,
  transports, modules and the composition root are the layers that observe.
- **Process identity is bound once, at startup, as OTel resource attributes** —
  `service.name` (`mosaic-platform`, `mosaic-supervisor`, and a name per module
  if modules ever leave the process), `service.instance.id`, and the Generation
  id. Mosaic is a single-host system with more than one process, so *which
  process said this* is a required dimension on every signal, not a nicety.
- **`fmt.Print*` and the standard `log` package are forbidden outside `main`'s
  own fatal path**, enforced by a boundary test in the same style as the existing
  transport boundary tests. The rule has to be executable or it decays back to
  where it is now.

## Alternatives considered

**Constructor injection of a logger.** *Rejected.* It is the conventional answer
and it fails on both counts. It is a mechanical change across most of 249 Go
files, it must be repeated for every new type, and — decisively — a
construction-time logger cannot carry request scope, so it solves the ceremony
problem badly and the correlation problem not at all.

**A package-level global logger.** *Rejected.* It removes the ceremony and keeps
none of the scope: every line is process-scoped, parallel tests interleave into
one sink, and there is nowhere to hang a trace id. It is `fmt.Printf` with JSON
punctuation.

**A logger parameter on the methods that need one.** *Rejected.* Strictly worse
than the constructor: the same threading, done per call, and the signature churn
lands on the contracts that
[platform#12](0012-published-contract-surface.md) is trying to keep stable.

**Put it in `diagnostics` rather than a new package.** *Rejected, narrowly.*
`diagnostics` is about *health and support bundles* — aggregating component state
and anonymising an export. Signals-in-flight is a different concern with a
different lifetime, and merging them would make the support bundle depend on the
OTel API. They share the redaction vocabulary and nothing else.

## Consequences

- **A function without a `ctx` cannot log.** This reads as a limitation and is
  the main benefit: it applies steady pressure to thread context through, which
  is exactly what tracing ([platform#32](0032-the-correlation-id-is-the-trace-id.md))
  needs anyway. Where a genuinely context-free helper must report something, it
  returns an error and lets its caller narrate.
- **Tests get a capture sink through the same door.** Seeding a context with a
  recording logger makes assertions on emitted telemetry ordinary table tests,
  with no global to reset between cases.
- **The `fmt.Printf` boot narration in `main.go` becomes structured**, which
  costs a little human readability at a terminal. The console sink therefore
  renders a friendly line format when attached to a TTY and JSON otherwise —
  a formatting choice at one sink, not a second logging path.
- **One import of the OTel API lands in the Platform tier.** It is Apache-2.0,
  stable at v1, and API-only, so it constrains nothing about which backend is
  used or whether one exists at all. It stops at the Platform: the SDK declares
  its own dependency-free surface for modules
  ([sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)) rather than re-exporting
  this one, so an implementation choice here never becomes a published contract.
- **The same ambient shape is what the module surface adopts.** `TelemetryFrom(ctx)`
  in the SDK is this decision applied across the module boundary, which is what
  lets one pattern cover eight entry points there and every call site here.

## Implementation implications

`internal/platform/telemetry` with `Into`/`From`, a `Logger` carrying bound
fields, levels, and the redaction-classed `Field` constructors moved from
`diagnostics`. Edge seeding lands with
[platform#33](0033-instrument-at-the-seams.md). The composition root configures the
sinks and the OTel SDK; `main.go`'s prints and `internal/transport/session`'s
`log.Printf` calls are the first two conversions, and the boundary test that
forbids their return lands in the same slice.
