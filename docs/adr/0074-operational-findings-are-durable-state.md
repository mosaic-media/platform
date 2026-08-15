# Operational findings are durable state

**Status:** Built. The register, its screen and the Supervisor's spool all landed; `unhealthy` and `unsupported` did not — see the roadmap.
**Date:** 2026-08-08

## Context

Mosaic reports every operational failure as a log line. A module that will not
start, an enrichment that failed for a whole catalogue, a Generation that was
activated and rolled back, a disk with a gigabyte left — each is a line in a
file, and each is gone from the user's view the moment it scrolls. Nothing
survives a restart, nothing is addressed to anybody, and nothing says what to do
about it.

That was tolerable while the Platform only answered requests. **M4 makes it
untenable**, because the Supervisor now takes decisions on the user's behalf
without being asked: it restarts a child that died, and it will activate a
Generation and roll it back when the new one fails its health check
([supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md)). A system that silently undoes
its own upgrade and says so only in a log is one where the user's report is "it
didn't update" and the truth is three days old and rotated away.

The [unreachable-capability register](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md) is a
document about the inverse problem — capabilities with no client path — and it
is maintained by hand, by people, about the build. It is not a runtime
mechanism and must not be confused with one.

**Home Assistant's Supervisor solved this shape and is worth naming.** It keeps
a resolution centre: an *issue* is a durable typed statement that something is
wrong, a *suggestion* is a named action that might fix it, a *fixup* is the code
that performs one, and the system carries `unhealthy` and `unsupported` states
with enumerated reasons. Its update path files an issue rather than only logging
when it rolls back. The specific lesson is not the taxonomy but that **the
failure and the remedy are one record**: a failure a user cannot act on is a
failure they will report to somebody else.

## Decision

**The Platform owns a resolution register: operational findings are durable,
typed, addressable state, created by the code that detects them and cleared by
the code that resolves them.**

- **An `Issue` is a typed statement that something is wrong**, with a context
  (what it is about — a module, a Generation, the host) and a reference to the
  specific thing. It persists until something clears it. It is created at the
  point of detection, by the code that detected it, and never by a scanner that
  sweeps for problems after the fact — a scanner can only find what it was
  written to look for, and it finds it long after the context is gone.
- **A `Suggestion` is a named action that might resolve an Issue.** It carries
  no prose: the client renders a suggestion type into whatever words and
  affordance suit it, which is what keeps this out of the SDUI's way and lets a
  future client translate it.
- **Applying a suggestion is an ordinary command**, authorised like any other
  through the policy gate. Repair is not a privileged back channel.
- **Issue types are a closed vocabulary.** The test in
  [platform#11](0011-open-and-closed-vocabularies.md) is "does Platform code branch
  on it?", and it does: a suggestion is offered *because* of the issue type. An
  open set would let a module state a problem no client can interpret and no
  code can act on, which fails open — the failure the whole vocabulary
  arrangement exists to prevent.
- **System-level state is separate from individual issues.** `unhealthy` (the
  Platform cannot be trusted to operate correctly) and `unsupported` (it is
  running outside what Mosaic supports) are distinct from a list of things that
  went wrong, and each carries enumerated reasons rather than a boolean.
- **The Supervisor spools its own findings to a file and hands them over.** It
  observes independently and file-only, merging into the Platform's plane when
  the Platform is up ([supervisor#5](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0005-the-supervisor-observes-independently.md)),
  and the findings it most needs to record — a Platform that will not start, a
  rollback — are exactly the ones it makes while the Platform is unavailable to
  be written to. The dependency does not invert: the Platform never reads the
  Supervisor's file to function, only to adopt what is in it.

## Alternatives considered

**Leave it as logs and telemetry.** Mosaic already has a structured telemetry
plane with a file sink and retention. It is genuinely good for answering "what
happened at 04:12", and genuinely useless for "what is wrong with my server
right now" — a log has no current state, no closure, and no addressee. The
existing plane stays; this is not a replacement for it.

**Health checks alone.** The Platform already reports component health and
readiness. Health is a binary about a component, not a statement about a
situation, and it carries no remedy: "postgres: degraded" cannot say "your disk
is full, delete a backup". Health answers "should traffic come here"; this
answers "what should a person do".

**Notifications or events.** The outbox already carries domain events, and an
event is a fact about a moment. An issue is a fact about *now* that must stop
being true when it is fixed. Modelling the second as a stream of the first puts
the burden of computing current state on every reader.

**A separate diagnostics service outside the Platform.** Rejected for the same
reason the Supervisor does not own extension modules
([platform#49](0049-the-platform-manages-extension-modules.md)): the code that can
detect a content-side problem lives in the Platform, and a second process would
have to be told everything the first already knows.

## Consequences

- **A new store and a new closed vocabulary**, both Platform-owned. Growing the
  issue-type set is a deliberate act, like every other closed set.
- **A screen.** Findings with no client path are the debt this document exists
  to stop accruing, so the register lands with a surface or it does not land.
  It is composed from the existing SDUI vocabulary; if it cannot be, that is a
  finding about the vocabulary and answered there
  ([contracts#2](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0002-primitives-and-definitions.md)), not by a bespoke component.
- **Every subsystem that fails now has somewhere to say so**, which is a
  standing invitation to file noise. An issue that nobody can act on is a log
  line with extra machinery: if there is no suggestion and no decision a user
  could take, it belongs in telemetry instead.
- **The Supervisor's spool is a file with no schema guarantees across
  versions.** It is written by one binary and read by another that may have been
  upgraded independently, so it must be treated as untrusted input and skipped
  rather than fatal when it cannot be parsed.
- **It does not make Mosaic self-healing.** A fixup is an action a user chooses.
  Automatic repair is a separate decision and is not taken here.
