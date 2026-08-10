# Instrument at the seams

**Status:** Accepted (built) — all nine seams, including seam nine:
`netguard.ModuleClient` is handed to every module in place of `nil`, which closed
the SSRF gap alongside the tracing.
**Date:** 2026-07-22

## Context

Observability efforts fail in a predictable way: the design is sound, the library
is chosen, and then someone has to add a span to every function. The first
hundred get added, the next thousand do not, and what remains is a partial map
that is worse than an honest blank one because its gaps are invisible.

Mosaic has an unusual defence against that, and it is worth naming before
choosing an approach. **The codebase is uniform where it matters.** The command
handler order is not a convention here, it is a stated rule every handler
follows:

> validate command shape → authenticate caller → authorize via policy → open a
> `UnitOfWork` → load state through contracts → apply domain rules → persist
> state and outbox events in the same transaction → return a Platform result
> type.

Every command handler in `internal/platform/app` has that shape. Every transport
is one of five packages. Every store write goes through one `UnitOfWork`. Every
module invocation goes through one registry method. Every error carries one of
seven categories.

That means the interesting events do not happen in a thousand places. They happen
in about nine, and each of those nine is a place a wrapper can sit.

## Decision

**Instrument the seams, not the call sites. A developer adding a command handler,
a resolver or a module writes no telemetry code and is fully instrumented.**

The nine seams:

1. **The session Connect interceptor** ([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md))
   — both lanes. Unary intents get a span each; `Subscribe` gets a long-lived
   span for the stream plus an event per pushed `RegionUpdate`, because a span
   per push on a stream that lives for hours is not a useful shape.
2. **The GraphQL handler** — one span per operation, named by operation, since it
   is now the external/tooling surface rather than the hot path.
3. **The HTTP origins** — artwork ([platform#20](0020-artwork-proxy-and-cache.md)),
   playback ([platform#25](0025-playback-consumer-and-media-origin.md)) and the
   Supervisor handoff.
4. **The command handler order itself.** One wrapper around the sequence gives
   every handler, present and future, a span with the authenticate and authorize
   steps as child spans and the error category as status. This is the seam that
   pays for the whole approach: the rule that made the codebase uniform is the
   rule that makes it observable.
5. **`contracts.UnitOfWork`** — a span per transaction, with commit and rollback
   distinguished. A wrapping implementation, chosen in the composition root, so
   `internal/modules/postgres` is untouched.
6. **pgx** — statement-level spans via `otelpgx`, as a child of the transaction
   span above.
7. **The outbox worker loop** — a span per drain, with the per-event links
   described in [platform#32](0032-the-correlation-id-is-the-trace-id.md).
8. **Capability invocation** — the module boundary, and the seam that does the
   most work. `CapabilityRegistry`'s invocation path is **wrapped, not edited
   inline**: a decorator around the `Capability` the registry holds. It spans the
   invocation, and it is also where the module's own telemetry surface
   ([sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)) is seeded into the
   context, where module attribution is stamped so a module cannot forge it, and
   where per-module quota is applied. Modules are statically composed today
   ([platform#4](0004-static-go-module-composition.md)) and moving them out of
   process over a socket is exploratory; if that happens, the decorator is
   replaced by a gRPC interceptor at the same seam rather than duplicated beside
   it.
9. **Outbound HTTP from modules.** A module receives an `*http.Client` at
   construction — `stremio.New(nil)` and `remoteplayback.New()` — and the
   composition root currently passes nothing, so each module builds its own
   untraced client. The composition root instead hands every module one client
   that wraps `otelhttp` over `internal/transport/netguard`'s dial guard. One
   change in `registerCapabilities` gives every module egress call a span **and**
   closes the SSRF gap that `netguard` exists to close but that module-built
   clients bypass.

Two rules keep the seams from eroding:

- **No telemetry in `domain`.** The domain applies rules; it does not narrate.
  This is [platform#31](0031-telemetry-is-ambient-in-context.md)'s dependency rule
  restated as a review criterion.
- **A span inside a handler is a smell, not a sin.** Where one is genuinely
  warranted — a long loop, a fan-out, an expensive parse — it is added
  deliberately and it is visible in review precisely *because* it is unusual.
  The seams are the floor, not a ceiling.

## Alternatives considered

**Hand-instrument every function.** *Rejected.* This is the failure mode
described above. It also inverts the cost: the highest-value spans (a request, a
transaction, a module call) are the cheapest to add at a seam, and the
lowest-value ones (a getter) are what manual effort tends to produce.

**Generate instrumentation from the contracts.** *Rejected.* Code generation
would pay off across hundreds of interfaces; there are nine seams, and a
generator is more machinery to maintain than the wrappers it would emit.

**eBPF or binary auto-instrumentation.** *Rejected.* Go's runtime makes this
fragile, it requires privileges a self-hosted media server should not ask for,
and it fundamentally cannot see domain concepts — it would report a syscall where
what is needed is "the Stremio module resolved a stream for this item".

**Middleware only, without the command-handler wrapper.** *Rejected.* Transport
middleware alone shows that a request took 900ms and nothing about where. Seam 4
is what turns a flat duration into an answer, and it is available only because
the handler order is uniform.

## Consequences

- **Coverage is structural.** A new command handler, resolver or module is
  instrumented the moment it exists, with no checklist and no review step to
  forget.
- **The wrapper must be transparent to error categories.** It observes and
  re-returns; it never reclassifies, wraps or swallows. A telemetry layer that
  changes a `Conflict` into an `Internal` would corrupt the contract that
  transports depend on.
- **Seam 9 fixes a real security gap as a side effect.** Modules building their
  own HTTP clients currently bypass the dial guard entirely. That is worth
  landing whether or not the telemetry work proceeds.
- **The `UnitOfWork` and `Capability` decorators must be exactly transparent** —
  same interface, same errors, same nil behaviour. They are thin by design and
  they need contract tests proving equivalence to the undecorated version.
- **Span volume needs a sampling policy from the start.** Nine seams over a busy
  session produce a lot of spans; sampling and the bounded-buffer discipline are
  settled in [platform#36](0036-telemetry-storage-retention-and-expert-mode.md).

## Implementation implications

Decorators live beside what they wrap or in `internal/platform/telemetry`, and
are applied in `cmd/mosaic-platform/main.go` — which is already the place that
chooses concrete implementations, so instrumentation becomes one more composition
decision. Seam 4 lands as a helper the handlers call once at entry rather than a
literal function wrapper, since the handlers are methods with varying signatures;
the effect is the same and it stays visible at the top of each handler. Seams 1,
4 and 8 are the first three and cover the multi-repository path end to end.
