# Audit is a store, not a log stream

**Status:** Accepted. **Partly superseded:** the claim below that a bootstrapped
administrator does not receive `audit.read` was reversed by
[platform#44](0044-privilege-cannot-escalate.md) — the first account is the
superuser and holds everything, since an action withheld from the only account
that exists can never be granted to anyone. The tier that lacks it by default is
the *administrator* preset. The rest of this record stands.
**Date:** 2026-07-22

## Context

The Platform publishes audit events today. `Service.publishAuditEvent` builds an
`Event` with `RedactionSensitive` and hands it to the event bus, and its own
comment states the contract:

> Publication is best-effort: a delivery failure must never mask the
> authorization or authentication outcome that triggered it, so the error is
> intentionally discarded.

That is the correct call for what it currently does — three event types
(`authentication.failed`, `authorization.denied`, `content.import.invoked`), each
a *signal* about a request that has already been decided. Losing one is
regrettable, not incorrect.

It is the wrong contract for an audit log. An audit record exists to answer, at
an arbitrary later date and possibly to someone hostile, *what happened to this
data and who did it*. That demands four properties the current path does not have
and was never trying to have:

- **Completeness** — every state change, not the three someone remembered.
- **Durability tied to the change itself** — a record that exists if and only if
  the change it describes was committed.
- **Tamper evidence** — no in-band way to alter or remove a record.
- **Its own access control and its own retention** — years, gated separately from
  everything else.

Logs have the opposite properties by design. They are lossy under pressure,
sampled, buffered, and rotated away in weeks. Storing audit records in the log
stream means inheriting every one of those, and the first time it matters will be
the first time someone asks a question the log has already discarded.

The atomicity requirement is not hypothetical. Mosaic already solved exactly this
for events with the transactional outbox: an `event_outbox` row and the state
change that produced it commit in the same transaction, so there is no window in
which one exists without the other. Audit needs the same guarantee, for the same
reason, and `Tx` already enumerates the Platform's stores
([platform#8](0008-capabilities-do-not-own-stores.md)) as the place a new one is
deliberately added.

## Decision

**Audit is a first-class Platform store with its own table, its own grants, its
own permissions and its own retention. It is not the log stream and it is not the
outbox.**

- **`audit_log`, reached through `Tx`.** An audit record is written inside the
  same transaction as the state change it describes, so the guarantee is exact:
  the change committed if and only if its audit record did. Adding a store to
  `Tx` is deliberate Platform evolution and should look like it — the content
  stores set that precedent.
- **Append-only enforced by the database, not by policy.** The application's
  PostgreSQL role receives `INSERT` and `SELECT` on `audit_log` and **not**
  `UPDATE` or `DELETE`. Retention deletion runs as a separate role. A rule the
  application could break is a rule that will eventually be broken by a bug; a
  missing grant cannot be.
- **Emitted at the persist step of the command handler order**, by the same
  mechanism that instruments it ([platform#33](0033-instrument-at-the-seams.md)).
  Coverage is therefore structural rather than remembered: a new command handler
  is audited because it is a command handler.
- **The record shape** is the actor, the action (the existing `policy.Action`),
  the resource, the decision, the outcome, the trace id from
  [platform#32](0032-the-correlation-id-is-the-trace-id.md), the module if a
  capability was acting, and a redaction-classed detail document
  ([platform#34](0034-redaction-classes-are-the-pii-boundary.md)).
- **Failures fail the command.** This is the deliberate reversal of today's
  best-effort contract: if the audit record cannot be written, the transaction
  does not commit. An action that could not be recorded did not happen.
  Authentication and authorization *denials* keep the existing best-effort path,
  because they change no state and must never be a way to make the Platform
  refuse service.
- **Read through the existing ABAC engine** — `audit.read` and `audit.export`
  actions, gated exactly like every other action, surfaced in expert mode
  ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md)). Access
  control on the audit log is not new machinery; it is the authorization system
  the Platform already enforces, pointed at itself. Note that a bootstrapped
  administrator does not receive these actions by default — audit reading is a
  grant, not an attribute of being an admin.
- **Retention is long, configurable, and floored.** An operator may set audit
  retention, but not below a compile-time floor of 30 days, and **a change to any
  retention setting is itself an audited action**. "Shorten retention, act, wait"
  is the standard way to defeat an audit log, and a floor plus a record of the
  attempt is what closes it.

## Alternatives considered

**Keep using the outbox.** *Rejected.* The outbox is delivery bookkeeping — its
rows carry `PublishedAt`, `Attempts`, `NextRetryAt`, `DeadLettered`, and they
exist to be drained. Audit records are never delivered anywhere and must never be
drained. Overloading one table with two lifecycles means the retention policy for
one becomes the retention policy for the other.

**Write audit records into the log stream.** *Rejected.* It inherits sampling,
buffering, loss under pressure and short retention — precisely the four
properties an audit log must not have — and it puts audit data under the log
viewer's access control rather than its own.

**A separate audit database, or an append-only file.** *Rejected.* Either breaks
atomicity with the state change, which is the single most important property
here. A record in another store can exist without its change, or the reverse, and
there is no transaction spanning both to prevent it.

**Hash-chain each record to its predecessor for tamper evidence.** *Deferred, not
rejected.* It is a real improvement and it is cheap to add later, since it is a
column. It is not worth blocking this on: an operator with database superuser
access can rewrite the chain as easily as the rows, so on a self-hosted
single-host deployment it raises the bar without changing the threat model. It
becomes worthwhile the moment audit records are exported somewhere the operator
does not control, which is where a SIEM integration would take them.

## Consequences

- **The write path gains a hard dependency.** Every command now writes an extra
  row in its transaction. The cost is one insert against a table with no foreign
  keys and one index, inside a transaction that is already open — small, and
  measurable rather than assumed.
- **A failed audit write becomes a failed command.** This is a genuine behaviour
  change and it will be visible the first time the audit table is full or its
  partition is missing. It is the correct trade, and it means partition
  management is operationally load-bearing.
- **Audit outlives everything else.** Logs and traces are gone in days;
  audit persists for a year or more. Its retention job, its partitions and its
  growth need to be sized on that basis, not on the log volume.
- **`audit.read` is a genuine escalation.** It reveals what every user did.
  Granting it is a decision, which is why it is separate from the admin bootstrap
  set rather than folded into it.
- **This unblocks nothing by itself, and needs the jobs runner.** Retention
  deletion and partition management are recurring no-user work, which needs the
  jobs runner, a scheduler and the **system principal**
  ([platform#13](0013-how-a-capability-acts.md)'s named gap) — the same blocked trio
  that [platform#28](0028-resolution-cache-and-capability-classes.md)'s refresh cron
  waits on. Two records now want it; it should be built once, for both.

## Implementation implications

Migration `0016_audit.sql` — the table, its indexes, and the role grants. An
`AuditStore` contract on `Tx` beside the existing accessors, implemented in
`internal/modules/postgres`. Emission from the persist step of the command
handler order. `ActionAuditRead`/`ActionAuditExport` in `internal/platform/app`,
deliberately absent from `adminPermissions()` in the composition root. The
retention job is deferred to the jobs-runner slice; until it exists, audit
retention is unbounded, which is the safe direction to be wrong in.
