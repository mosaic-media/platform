# The upgrade channel is the handoff and the register

**Status:** Built. The Supervisor checks the catalogue on a schedule and spools an offer, polls `GET /upgrade` and carries out what it finds; the Platform raises `upgrade_available`, offers `apply_upgrade`, records the request and settles it by comparing `MOSAIC_GENERATION_ID` against the version asked for. Not built: [supervisor#12](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0012-upgrade-automation-is-staged-against-the-contract-version.md)'s automation *policy* on top of it — every upgrade is a person pressing something today, which is that record's Manual level and its safest.
**Date:** 2026-08-09

Closes the question [supervisor#12](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0012-upgrade-automation-is-staged-against-the-contract-version.md)
deliberately left open — *how does a person's choice reach the Supervisor* — and
with it the one clause of M4's exit criterion that did not land.

## Context

The Supervisor can fetch a Generation, verify it, activate it, gate on the
surface a client actually reaches, and revert keeping the evidence. All of it
works and all of it is reachable **only from Go**. Nothing polls the catalogue
and no surface offers the check or the upgrade, so an install that could upgrade
itself safely has no way to be told to. It has been a row on the
[unreachable-capability register](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md) since the
mechanism landed.

[supervisor#12](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0012-upgrade-automation-is-staged-against-the-contract-version.md) decided the *policy* — three levels, and a contract change is never
activated unattended — and named the obstacle in its Consequences: "The setting
is Platform configuration and the actor is the Supervisor, which is the one place
they must agree and the Supervisor cannot read the Platform's database. How the
choice reaches it — the handoff channel, an environment variable, a file — is an
implementation question this leaves open."

That question has looked harder than it is, because the two channels it needs
already exist and are already used for exactly this shape of problem:

- **The handoff surface** is how the Supervisor asks the Platform things. It
  already reports *what escalation is owed*: M4's configuration versioning
  records a `pending` version whose reload class exceeds what a running Platform
  can apply, and `GET /config` is how the process that can perform the escalation
  finds out one is waiting. An upgrade is the same shape — a request the Platform
  records and cannot itself carry out.
- **The findings spool** is how the Supervisor tells the Platform things
  ([platform#74](0074-operational-findings-are-durable-state.md)). It already
  carries what the Supervisor learned while the Platform was not there, and
  [supervisor#12](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0012-upgrade-automation-is-staged-against-the-contract-version.md) already anticipated its use here: "A staged Generation is a finding:
  the register is how an install says 'there is a version here you have not
  taken', with the suggestion being to apply it. That is what makes the offer
  reachable without inventing a notification channel."

There is also a smaller gap in the way. The Platform reads `MOSAIC_GENERATION_ID`
into its telemetry resource and **nothing sets it** — the Supervisor never passes
it to a child. So a running Platform cannot say which Generation it is, which is
the fact that decides whether a requested upgrade has happened.

## Decision

**The offer travels on the spool, the request travels on the handoff, and the
Generation id is what settles it. No new channel is invented.**

- **The Supervisor checks the catalogue on a schedule and records an offer as a
  finding.** `Updater.Check` already answers "what is available" without changing
  anything, and a finding is already a durable typed statement with a suggestion
  attached. So an available version reaches a person as a row on Settings ›
  Problems, which is the surface that already exists for "something needs your
  attention", rather than as a notification channel built for one feature.
- **Pressing the suggestion records an upgrade request naming a version.** Not
  "latest": the Platform does not hold the catalogue and must not guess at what
  the newest version is, so the version it asks for is the one the finding
  offered it. `Updater.UpgradeTo` already takes a named version and resolves its
  URL from the *signed catalogue* rather than from the caller, so no request can
  point an install at bytes nobody signed for.
- **The handoff reports the pending request, and the Supervisor polls it.**
  `GET /upgrade` beside `GET /config`, on the same private listener, which
  [platform#75](0075-the-children-listen-on-unix-sockets.md) keeps off the public port precisely so Generation state is not
  published. The Supervisor is already dialling that listener every couple of
  seconds for readiness.
- **A request settles when the Platform is running the version it asked for.**
  The Supervisor passes `MOSAIC_GENERATION_ID` to its children, so a Platform can
  compare what was requested against what it *is*. That makes settlement a fact
  rather than an acknowledgement, and it is why the channel stays one-directional:
  the Supervisor never has to report success, because success is observable.
- **Failure travels back on the spool, as a finding.** An upgrade that would not
  activate has already reverted and already writes `generation_rolled_back`; what
  this adds is that the request stops being pending, because the Platform that
  comes back is the old one and its Generation does not match. The offer returns
  on the next check, which is correct: the version is still available and still
  did not install.
- **The policy of [supervisor#12](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0012-upgrade-automation-is-staged-against-the-contract-version.md) sits on top of this and is not built here.** This
  record decides the *channel*; whether an install takes a small upgrade
  unattended is a setting, and it reads the contract version rather than the
  artefact's ([supervisor#12](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0012-upgrade-automation-is-staged-against-the-contract-version.md)).
  An automatic level would skip the finding and go straight to the request, using
  the same two channels.

## Alternatives

**A new RPC from the Platform to the Supervisor.** *Rejected.* It inverts the
dependency the whole arrangement is built to avoid: the component that survives
would take a call from the component that does not, and it would need a listener,
an address, authentication between two processes that already have a private
channel, and a failure mode for "the Supervisor is not answering" that the
Platform has no useful response to. The handoff already goes the safe way round.

**An acknowledgement posted back on the handoff.** *Rejected*, and it was the
first shape tried on paper. It makes the channel two-directional for one message,
and it has an ordering problem with no clean answer: the upgrade *replaces* the
process that would record the acknowledgement, so an ack written before the
switch is a lie if the activation reverts, and one written after is written by a
process that may not be the one that was asked. Comparing the running Generation
id has neither problem, because it is a statement about what *is* rather than
about what happened.

**A file in the state directory, written by the Platform.** *Rejected.* It is the
spool's shape pointed the other way, and the two processes do not share a
writable directory by design — the Platform's working directory and the
Supervisor's state directory coincide only in the shipped image, not on the DIY
path or in the dev stack. A channel that exists in one deployment of three is not
a channel.

**Put the control on the Supervisor's own recovery screen.** *Rejected.*
[supervisor#7](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0007-the-supervisor-answers-the-platforms-client-surface.md) has the
Supervisor answer the Platform's client surface only while the Platform is
*down*, and an upgrade is something you do to an install that is up. A control
that only appears when Mosaic is broken is the wrong place for the routine act.

## Consequences

- **The register row for upgrading is dischargeable**, and with it the last
  clause of M4's exit criterion. A person can see that a version is available and
  press something.
- **`MOSAIC_GENERATION_ID` becomes load-bearing rather than decorative.** It was
  read and never written; it now decides whether a request is satisfied. A
  deployment that runs the Platform itself — the DIY path — sets no Generation id,
  so a request made there never settles by this rule. That is honest: nobody is
  managing Generations in that deployment, so nothing was going to carry out the
  request either.
- **The offer repeats until it is taken or the version stops being offered.**
  The register folds repeats into one Issue with a count and a `first_seen`, so
  "there has been an upgrade available since Tuesday" is a thing an install can
  say. It also means a user who does not want a version has no way to dismiss it
  permanently; that is a gap this record names rather than solves, and the
  obvious answer — a snooze — is a setting nobody has asked for yet.
- **A check is a network call on a schedule**, which the Supervisor has not made
  before. It is failure-tolerant by construction (an unreachable catalogue costs
  a check, not an upgrade) and it is the first thing in that process that reaches
  outward on its own, which is worth knowing rather than discovering.
