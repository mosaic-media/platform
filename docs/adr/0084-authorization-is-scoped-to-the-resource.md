# Authorization is scoped to the resource, not only the action

**Status:** Accepted. **Not built.** Prerequisite for
[platform#85](0085-a-modules-authority-is-declared-and-consented.md), which
cannot enforce a grant until this lands.

## Context

`policy.Engine.Authorize` has carried an ABAC-shaped signature since it was
written:

```go
func (e *Engine) Authorize(ctx context.Context, subject Subject, action Action, _ Resource, _ PolicyContext) (Decision, error)
```

Two of the four parameters are discarded. `Resource{Type, ID}` and
`PolicyContext` exist as types, are passed by every caller, and decide nothing.
Every rule in the system is therefore of the form *may this subject perform this
action*, never *upon what*.

That was the right shape while every action was install-wide. `user.create` and
`telemetry.read` do not have an interesting object: holding them at all is the
decision. The shape stops being sufficient the moment a subject should hold an
action over some things and not others, and three separate pieces of work now
need exactly that:

- **Module authority.** A module granted `content.write` would hold it over
  every node in the library rather than over the ones it has anything to do
  with ([platform#85](0085-a-modules-authority-is-declared-and-consented.md)).
- **One library, many viewers.** [platform#59](0059-one-library-many-viewers.md)
  made what a viewer sees their own. What a viewer may *reach* is the same
  question one level down, and parental controls are its sharpest form.
- **Module-owned things.** A module's own storage and its own settings document
  have an unambiguous owner, and a grant over "settings" that means *every
  module's settings* is not a grant anybody would write deliberately.

Discarding the parameter has also had a quiet cost: because `Resource` decides
nothing, no call site has ever been checked for passing the right one. Several
pass a zero value. Turning the parameter on turns every one of those into a
decision that was never reviewed.

## Decision

**`Authorize` honours `Resource`, for every subject, not only for modules.**

A rule may name a resource type, a resource id, or neither. A rule naming
neither continues to mean *any resource of any type*, which is what every rule
means today — so the existing corpus keeps its current meaning without being
rewritten.

**Scoping is uniform across subject kinds.** A user's grant, the system
principal's, and a module's are evaluated by one code path against one rule
shape. The alternative — scoping that applies only when a module is the subject
— was rejected: it puts two authorization models in one engine, and the second
one is exercised only by the newest and least-trusted kind of caller.

**Every call site passes the resource it is acting upon.** A zero `Resource` is
a positive statement that the action has no object, not a default to fall back
on when the answer is inconvenient.

**The boundary conformance suite grows to cover it.**
[platform#41](0041-authorization-is-carried-in-the-type.md)'s reflection pass
already fails the build when a caller-bearing method appears in neither
`boundaryCases` nor `boundaryExempt`. It gains the resource dimension: a method
that acts upon an object is called with a subject holding the action over a
*different* object and must answer `PermissionDenied`. Without that, a call site
passing a zero resource where it should pass a real one is indistinguishable
from one that is correct.

## Alternatives considered

**Leave `Resource` ignored and scope nothing.** The smallest change, and it is
what the code does today. Rejected because the three pieces of work above all
need it, and because a discarded parameter that every caller populates is a
standing invitation to assume it works.

**Scope module grants only.** Attractive: no existing call site changes, and the
conformance suite stays as it is. Rejected on the grounds above — two models in
one engine, with the untested one guarding the least-trusted caller — and
because per-viewer restriction wants the same mechanism and would then need a
third.

**Express scope as a predicate rather than a resource.** A rule could carry a
condition evaluated against the object — *nodes this subject created*, *parts
below this node*. Strictly more expressive. Rejected for now because it makes
`Authorize` read stores to answer, turning a pure decision into one with a
failure mode and a latency, and because no case yet needs more than type and id.
The rule shape does not preclude it later.

## Consequences

- **Every existing call site is touched.** This is the bulk of the work and
  most of the risk. A call site that passes the wrong resource fails closed if
  the rule is narrow and open if it is not, and only the conformance suite's new
  dimension distinguishes the two.
- **Rules gain a shape that can be got wrong.** A rule naming no resource is
  install-wide, which is the current meaning and the permissive one. That is the
  right default for compatibility and the wrong default for safety, so a new
  action's rule should be written narrow and widened deliberately.
- **`PolicyContext` stays discarded.** It is out of scope here. Turning on one
  ignored parameter is enough for one record, and the attributes it would carry
  — admin mode, recovery mode — have no rule that reads them yet.
- **Parental controls and per-viewer reach become expressible**, which they are
  not today. Neither is built by this record.
