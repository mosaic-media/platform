# First-administrator bootstrap

**Status:** Accepted. Expected to be superseded when Supervisor onboarding exists.
**Date:** 2026-07-20

## Context

The Platform serves an authenticated API: every command and query passes
through policy, and content, user and role operations require a caller who
already holds the relevant permission. That is correct, and it produces a
chicken-and-egg problem at the very beginning. There is no in-band way to
create the first administrator, because every path that could assign authority
— `CreateRole`, `GrantRole`, even signing in (which needs `session.create`) —
is itself policy-gated and needs an already-authorised caller. A fresh database
has no such caller.

Something has to seed the first authority from outside the authenticated
surface. The question is what, and how much to build for it now.

The natural long-term owner is the **Supervisor**, via **Shell onboarding**
([supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md), [supervisor#2](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0002-supervisor-guarantees-an-interface.md), [supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)): before the first Generation is even built, an operator would run through onboarding, choose the optional modules, and provide the initial administrator — after which the Supervisor builds the binary and runs it as Generation 1. That flow does not exist yet; only the Platform's half of the Supervisor handoff does.

## Decision

**The composition root seeds the first administrator directly, as a deliberate
bridge until Supervisor onboarding owns it.**

`internal/composition/bootstrap.EnsureAdmin` creates a user, an Argon2id
password credential, an `Administrator` role carrying the full action set, and
the grant binding them — all in one transaction — unless a user with that
username already exists, in which case it is a no-op. `main.go` runs it when
`MOSAIC_BOOTSTRAP_ADMIN_USERNAME` and `MOSAIC_BOOTSTRAP_ADMIN_PASSWORD` are both
set; the password is read once and never logged.

Two properties make this defensible rather than a bodge:

1. **It writes through the store contracts, not raw SQL.** The one thing the
   composition root does that no application service may — assign authority
   with no authenticated caller — still goes through `Tx` and the
   `PermissionStore` write methods, transactionally, so a partial admin can
   never be left behind.
2. **It is the seam, not the mechanism's final home.** `EnsureAdmin` is a
   primitive: "on first boot, if no admin exists, create one." Whatever drives
   it — a human setting env vars today, Supervisor onboarding tomorrow — the
   seeding logic is the same.

## Alternatives considered

**Wait for Supervisor onboarding.** The clean end state. *Rejected for now:* it
leaves the Platform unusable by a human in the meantime — it serves an API
nobody can obtain a session for — and the Supervisor build/onboarding surface
is a large, unstarted body of work. A bridge that is honestly a bridge is worth
more than an unusable binary.

**Raw SQL in the composition root.** The shortest path, since `PermissionStore`
was read-only when this need first appeared. *Rejected:* it reaches past the
contracts into another module's schema for a capability the Platform ought to
have. That objection is what motivated the `CreateRole`/`GrantRole` write path,
which this now uses instead.

**No bootstrap; document a manual seeding step.** *Rejected:* it pushes the
chicken-and-egg problem onto every operator and every test, and a hand-run SQL
step is exactly the kind of out-of-band state this project avoids.

## Consequences

**The running binary is usable by a human now.** Set the two env vars, start
the process, and there is an administrator who can sign in and manage everyone
else through the normal, authenticated commands.

**This is expected to be superseded.** When Supervisor onboarding lands, the
first-administrator setup moves there: onboarding collects the operator's
choices and the initial administrator, the Supervisor builds and launches
Generation 1, and the credential is injected through a channel more suitable
than a plaintext environment variable — most likely a `secret://` reference
resolved by the Secret Broker, or standard input, or one-time material. That
change is a superseding ADR (probably out of an RFC that designs the whole
onboarding flow), and it changes only *how the credential arrives*:
`EnsureAdmin` stays the seam.

**One open question is deliberately left to that work:** the exact injection
channel between Supervisor onboarding and the Platform. It is recorded in the
overview's *Deliberately Undecided* rather than settled here, because there is
no worked onboarding design to decide it against yet.

**The env-var channel is bridge-grade, and known to be.** A plaintext
environment variable is visible in process listings and inherited environments;
it is acceptable for initial bootstrap by an operator who controls the host, and
it is one of the reasons this decision anticipates its own replacement.
