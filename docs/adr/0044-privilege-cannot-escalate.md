# Privilege cannot escalate through delegation

**Status:** Accepted (built)
**Date:** 2026-07-23

## Context

Mosaic's authority is granular. What an account may do is the set of actions
granted to it, checked one at a time by the policy engine; nothing reads a
role's *name* to decide anything. That is the right model, and it had a hole
big enough to make the rest of it decorative.

`CreateRole` accepted any permission set and `GrantRole` bound any role to any
user, neither checking what the *caller* held. So `role.create` was not the
action "create a role". It was the action "hold every permission", reachable in
two steps: mint a role carrying everything, grant it to yourself. Every careful
distinction elsewhere — that `telemetry.read` reveals what every user did, that
`config.activate` changes how the process runs — was undone by one action that
could confer them all.

[platform#14](0014-first-admin-bootstrap.md) seeds a first account on first boot,
because every command that could grant authority is itself gated and something
must break the cycle. It called that account an "Administrator" and gave it
everything.

Two later records then reasoned about who should hold the most sensitive
actions and both reached the wrong answer.
[platform#35](0035-audit-is-a-store-not-a-log-stream.md) and
[platform#36](0036-telemetry-storage-retention-and-expert-mode.md) say
`audit.read` and `telemetry.read` are withheld from the *bootstrapped account*,
on the grounds that being first should not confer the ability to watch everyone.
The instinct is right and the target is wrong: withholding an action from the
only account that exists produces a permission **nobody can hold and nobody can
grant**. The expert-mode surface shipped with its only access path being a
hand-written `UPDATE` on the `roles` table.

## Decision

**Nobody may grant authority they do not themselves hold. Presets are a
convenience for whoever is granting, not a tier system.**

- **The delegation rule is the boundary.** `CreateRole` refuses a permission set
  that is not a subset of the caller's own effective permissions. `GrantRole`
  refuses a role whose permissions are not a subset of the caller's. Both are
  checked against current state at the moment of the grant, and the refusal
  names the missing permissions — someone assembling a role from twenty
  checkboxes cannot act on "denied", and the information is not sensitive since
  they know their own grants and chose the set.
- **Granting is bounded at both ends.** Creating a role and granting one are
  separate acts, so both are checked. A role the superuser created must not
  become an escalation path for an administrator who can grant but does not hold
  what it carries.
- **Presets are starting points.** `Superuser`, `Administrator` and `User` are
  named bundles a grantor begins from and then edits. Nothing enforces them,
  nothing reads them at authorization time, and an account's authority is
  whatever it was granted regardless of which preset produced it.
- **The offer is narrowed server-side.** `GrantablePermissions` returns what a
  grantor may confer — exactly what they hold — and which of it a preset starts
  ticked. **A grantor never sees a permission they lack**: absent, not disabled.
  A screen showing a box the server will refuse teaches people the product is
  broken; a screen showing a box the server *would* honour is the escalation
  itself.
- **Presets nest.** `User` ⊆ `Administrator` ⊆ `Superuser`, so choosing a
  smaller preset is always a reduction and never a sideways move that quietly
  adds something.
- **The first account is the superuser and holds everything.** It is the root of
  every other grant, so an authority withheld from it could never reach anyone.
  This is what corrects ADRs 0057 and 0058: the tier that does not get insight
  *by default* is the administrator, and "by default" means the preset, not a
  prohibition.

The rule composes without special cases. An administrator whose own set was
trimmed can still create accounts, and can only pass on what survived the
trimming — a `User` preset granted by a reduced administrator is silently the
intersection, which is the correct answer and needs no code of its own.

## Alternatives considered

**Enforce it in the interface only.** *Rejected.* The command surface is
reachable without going through a screen — that is the whole point of [platform#37](0037-one-client-transport.md)'s
one client transport being the application services. An interface is where an
authority boundary is *displayed*, never where it lives.

**A `superuser` boolean that bypasses policy.** *Rejected.* It puts a second
authorisation mechanism beside the one the Platform already enforces, and a
bypass appears in no authorization span and no audit record — so the most
privileged account would be the least observable. A superuser that simply holds
every action is checked by exactly the same path as everyone else.

**Tie authority to the role's name.** *Rejected.* Authority would depend on a
string, and a rename would change permissions silently. The name is a label; the
grants are the authority.

**Fixed tiers, with administrators structurally unable to hold certain
actions.** *Rejected.* It cannot express "an administrator who may also read traces", which is an ordinary
thing to want, and it puts the boundary in the wrong place: the danger was never
that an administrator might hold `telemetry.read`, it was that anyone could
*give themselves* anything.

**Let a grantor confer anything and audit it afterwards.** *Rejected.* Audit
records what happened; it does not prevent it. Self-promotion detected after the
fact is self-promotion.

## Consequences

- **`role.create` becomes the action it is named after.** Before this it was
  equivalent to every permission.
- **ADRs 0057 and 0058 are corrected rather than quietly contradicted.** Both
  said the bootstrapped account is denied these actions; it is not, and the
  paragraph in each now says so and points here.
- **Two existing tests were demonstrating the hole.** `roleFixture` created a
  role carrying content permissions its caller did not hold, and the bootstrap
  test seeded an account with `role.create` alone and then minted `content.read`
  from it. Both now hold what they delegate. A test that passes because the
  system is permissive is worth more once it fails.
- **The administrator preset can grant.** Managing accounts is ordinary
  administration, and it is safe *because* delegation is bounded — which is the
  substitution this record makes for a tier that structurally could not.
- **None of it is reachable yet.** There is no client surface for creating a
  user or granting a role, which remains the
  [unreachable capability](../unreachable-capability.md) register's largest
  entry. `GrantablePermissions` exists, is tested, and has no screen — stated
  rather than implied.
- **Presets will drift from the UI's vocabulary unless the UI reads them.**
  They are served by the Platform for exactly that reason; a client that
  hard-codes its own list of "admin permissions" reintroduces the disagreement
  this avoids.

## Implementation implications

`internal/platform/app/delegation.go` holds `ensureCanDelegate` and
`ensureCanDelegateRole`, called from `CreateRole` and `GrantRole` after the
boundary is entered. `PermissionStore` gained `FindRole`, since a grant that
cannot see what it is granting cannot bound it; a missing role is left to the
store's existing `Conflict` rather than being rewritten to `NotFound` by a check
added for an unrelated purpose. `roles.go` holds the presets and `Preset(name)`;
`grantable_permissions.go` serves the narrowed offer. `main.go` seeds the first
account from `SuperuserActions()`, and `bootstrap.EnsureAdmin` names its role
`Superuser`. The audit actions join `superuserActions` when
[platform#35](0035-audit-is-a-store-not-a-log-stream.md) is built.
