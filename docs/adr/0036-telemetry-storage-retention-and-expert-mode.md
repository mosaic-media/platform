# Telemetry storage, retention, and expert mode

**Status:** Accepted (built for logs and spans). Both sinks exist, the
PostgreSQL side lands in migrations `0014` and `0015`, and expert mode serves the
log viewer and the trace waterfall behind `telemetry.read`, with the affordance
hidden from anyone without it. Retention deletion was built in M0.1: the hourly
`telemetry.retention` job runs as the system principal
([platform#13](0013-how-a-capability-acts.md)), extends the partition window and
drops what the Active configuration's retention has run out on. **Unbuilt:** the
metrics and audit surfaces, since neither signal is produced yet
([platform#35](0035-audit-is-a-store-not-a-log-stream.md)).
**Date:** 2026-07-22

## Context

The previous five records decide how telemetry is *produced*. This one decides
where it goes, how long it stays, and who can look at it — and that last question
is what settles the first two.

The instinctive answer to "where does it go" is the standard stack: an OTel
Collector, Prometheus for metrics, Loki or OpenSearch for logs, Tempo or Jaeger
for traces, Grafana over all of it. It is the right answer for an operations team
running many services. Mosaic is a **self-hosted media server**: a Platform
process, a Supervisor, a browser client, and PostgreSQL, on one host, in one
trust domain, usually owned by one household. Asking that user to run five more
containers to find out why an import failed is asking them not to bother.

But "ship JSON lines and let the operator point Promtail at them" is a worse
answer, not a better one, because it assumes an operator who wants to run a
telemetry stack. **Mosaic has an admin UI.** The honest requirement is that an
administrator can turn on an expert mode inside Mosaic and see their own logs,
traces and metrics — no external tooling, no container, no YAML.

That requirement decides the storage question that a file sink alone cannot
answer. Flat JSON lines are excellent for durability and useless for "show me
errors from the Stremio module in the last hour, for this trace". A queryable UI
needs indexed, time-bounded, queryable storage.

Mosaic already depends on one: PostgreSQL is not optional, it is the storage
module, and it is running on the same host. The volumes involved are not
web-scale — a household media server produces on the order of tens of megabytes
of telemetry a day, which is the scale Postgres handles without noticing and the
scale at which Loki's object-storage indexing buys nothing.

There is one thing Postgres cannot do, and it is important: **it cannot record
why it is down.** Telemetry written only to the database is missing exactly when
it is most needed.

The UI surface, unusually, already exists. `internal/transport/screens/settings.go`
hosts a Platform-owned settings frame ([sdk#4](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0004-module-contributed-settings-ui.md)),
and the session transport already pushes region updates to a live client
([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)) — so a live log tail is
a consumer of push machinery that is already built, not new transport work.

## Decision

**Dual-sink storage, an in-product expert-mode surface, and per-signal retention
an administrator controls — with export to an external stack optional and
additive.**

- **Two sinks, neither optional.**
  - **File** — always written, JSON lines, size- and age-rotated. It is
    crash-survivable, it is the source of truth for the support bundle, and it
    works when PostgreSQL does not. It is what records the database failing.
  - **PostgreSQL** — written additionally, and *only* to make the UI queryable.
    Partitioned by day, BRIN index on timestamp, per-signal retention. If it is
    unavailable the UI says so, which is itself a diagnosis, and nothing is lost
    because the file has it.
- **The telemetry write never blocks a request.** Both sinks are fed from a
  bounded in-memory buffer drained by a background writer. Under pressure it
  drops oldest-first and increments a dropped-record counter that is itself
  telemetry. A user's playback must never wait on a log insert, and a telemetry
  subsystem that can stall the Platform is a liability rather than an asset.
- **Expert mode is a Platform-emitted SDUI screen**, a sibling of
  `settings.go` — not a module contribution, so
  [sdk#4](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0004-module-contributed-settings-ui.md)'s bounded exception is not
  widened. It offers:
  - a **log viewer** — filter by process, component, level, module, trace id and
    time window, with live tail as a `RegionUpdate` on the existing Subscribe
    lane;
  - a **trace waterfall** — the span tree for one trace id. This is the
    multi-repository payoff: one screen showing shell click → session intent →
    command handler → module → upstream HTTP → SQL;
  - **metrics** — a small set of sparklines;
  - the **audit browser** ([platform#35](0035-audit-is-a-store-not-a-log-stream.md)),
    on its own permission;
  - **support bundle export**, over the existing anonymising machinery.
- **No query language.** Filters and trace-id lookup, and that is the whole
  surface. Building a query language is how this becomes a year of work instead
  of a slice; anyone who needs PromQL should turn on OTLP export and use the tool
  that already has it.
- **Two independent gates, and they are not the same thing.** `telemetry.read`,
  `telemetry.export`, `telemetry.configure` and `audit.read` are **ABAC actions**
  on the existing policy engine. **Expert mode is a per-user preference** that
  reveals the surface. The toggle controls visibility; the permission controls
  data. A user who flips the toggle without the grant sees a denial, not a leak —
  conflating the two is how a debug switch becomes a disclosure.
- **Retention is per-signal, configured, and reload-classed.** Defaults: logs 14
  days, traces 72 hours, metrics 30 days at one-minute resolution, audit 400
  days. Each is a `config` field with a declared reload class — `Hot`, since a
  retention change should not need a restart — editable in expert mode under
  `telemetry.configure`. **Audit is floored at 30 days in code, not in config,
  and every retention change is an audited action**
  ([platform#35](0035-audit-is-a-store-not-a-log-stream.md)).
- **OTLP export is opt-in and changes nothing else.** Setting an endpoint streams
  the same instrumentation to a Collector; leaving it unset costs nothing. The
  full Prometheus/Grafana/Tempo/Loki stack ships as a `docker-compose.obs.yml`
  **development** profile — which is what makes cross-repository debugging
  pleasant for contributors without making it a requirement for users. SIEM
  export is a further audit exporter, deferred until a deployment needs one
  rather than guessed at now.

## Alternatives considered

**Bundle Loki, or ELK/OpenSearch.** *Rejected.* Container weight and operational
surface a self-hosted user will not accept, in exchange for indexing at a scale
Mosaic will not reach. The in-product viewer is also the better *product*
answer — an administrator should not have to leave Mosaic to debug Mosaic.

**Files only, with external tooling for anything more.** *Rejected*, and this was
the earlier position this record corrects. It is a reasonable answer for a
daemon and a cop-out for a product with an administrative interface.

**PostgreSQL only.** *Rejected.* It loses telemetry precisely when PostgreSQL is
the fault, which is the case where it matters most. The file sink costs almost
nothing and removes that blind spot entirely.

**An embedded store — SQLite, DuckDB — for telemetry.** *Rejected.* A second
storage engine means a second backup story, a second migration story, a second
failure mode and a second thing to explain, for a workload the existing database
handles. [platform#10](0010-storage-authority-and-transaction-scope.md)'s single
storage authority is worth more than the marginal fit.

**Write telemetry through the `UnitOfWork` like everything else.** *Rejected.*
Telemetry is high-volume, lossy-by-design and must never fail a request — the
opposite of every property the transaction shape exists to guarantee. It gets a
direct pooled writer outside `Tx`. Audit is the deliberate exception, and it is
in `Tx` for exactly the reasons telemetry is not.

**Let administrators set audit retention freely, including to zero.**
*Rejected.* It converts the audit log into something an attacker with admin
access can erase in advance. The floor is a small constraint on a legitimate
operator and a real obstacle to an illegitimate one.

## Consequences

- **Telemetry write volume lands on the content database.** It must be measured
  before this is called done, and it must be bounded — the drop-oldest buffer is
  what guarantees a telemetry surge degrades telemetry rather than the Platform.
- **Partition creation and retention deletion are recurring no-user jobs.** Like
  [platform#35](0035-audit-is-a-store-not-a-log-stream.md), this needs the jobs
  runner, a scheduler and the **system principal**. Until they exist, partitions
  are created eagerly ahead of time and retention is manual — a stated gap, not a
  silent one.
- **Rendering telemetry into a browser is defensible only because of
  [platform#34](0034-redaction-classes-are-the-pii-boundary.md).** Records are
  redacted at construction, so the viewer has nothing left to leak. If that
  record is weakened, this one must be reconsidered with it.
- **The trace waterfall is the feature that repays the whole effort.** It is also
  the one that most depends on
  [platform#32](0032-the-correlation-id-is-the-trace-id.md) being complete — a
  waterfall with a missing hop is worse than none, because it implies the hop did
  not happen.
- **A second client renders expert mode for free.** It is SDUI like every other
  screen, so a future Flutter client gets the diagnostics surface without
  additional work — which is the same argument that put content screens on the
  emit-side ([platform#19](0019-sdui-emit-side.md)).
- **The Supervisor observes itself, separately** ([supervisor#5](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0005-the-supervisor-observes-independently.md)).
  Neither sink here covers a Platform that never starts, because both live inside
  it. The Supervisor keeps its own file-only telemetry for that case and merges
  into this surface when the Platform is up; the Platform never depends on it.
- **Module records land in these sinks too** ([sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)),
  attributed and quota-bounded by the Platform. The viewer must render
  module-supplied text as untrusted content, since it originates outside the
  trust boundary.

## Implementation implications

Migration `0014_telemetry.sql` — partitioned `telemetry_logs` and
`telemetry_spans`, a `telemetry_metrics` rollup, BRIN indexes on time, and the
partition helpers. A bounded ring buffer and background writer in
`internal/platform/telemetry`, fed by the seams of
[platform#33](0033-instrument-at-the-seams.md). `internal/transport/screens/diagnostics.go`
for the expert-mode screens, and the query services behind them in
`internal/platform/app` — resolvers and screens call services, as always. New
actions in the app package's action set. Retention fields join
`config.PlatformSchema()` with their reload classes. `docker-compose.obs.yml` is
a developer convenience and lands last.
