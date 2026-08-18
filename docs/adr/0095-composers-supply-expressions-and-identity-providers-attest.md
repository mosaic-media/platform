# A composer supplies an expression, and an identity provider attests

**Status:** Accepted. **Not built.** Depends on
[platform#88](0088-a-contribution-composes-from-published-definitions.md) and
[platform#85](0085-a-modules-authority-is-declared-and-consented.md). Slice 9 of
the extension surface, and the last of them.

## Context

Two unrelated capabilities share this slice.

**Composers** derive new experiences from what is already in the library.
`ListLibrary` was deliberately kept off the SDK: paging and a total are what a
browse needs and what nothing sourcing content ever asked for, so they did not
grow the surface every installed extension holds — the same reasoning that kept
watch history off `ContentService`. That reasoning held for sources. A module
deriving something from the library is not sourcing, which is why the question
returns here.

There is already an answer on record for the sharpest case. On watch history:
"an anime-scoped continue-watching rail is the Platform composing and the module
supplying the filter."

**Identity providers** are the larger expansion. Identity is wholly
Platform-owned, and this is the first thing that would give a module any part in
deciding who somebody is.

## Decision

### Composers

**A composer supplies criteria; the Platform holds the data.** It never receives
library rows and never receives watch history. This generalises the answer
already given for the continue-watching case rather than inventing a second rule.

**Criteria are not limited to a filter.** A composer may supply a **declarative
expression** — matching, scoring, ranking, a pattern over what a person has
watched — which the Platform evaluates. Restricting a composer to a flat filter
would have been an unnecessarily narrow reading of the same principle: an
algorithm is not the same thing as access to the data it runs over, and a module
can offer the first without ever being given the second.

**The expression is declarative and the Platform evaluates it. Module code is
never fed the rows.** This is the line that makes the distinction real rather
than rhetorical: a module process holds network egress and storage, so handing it
watch history to score *is* granting access, whatever the calling convention
looks like. What crosses the boundary is a description of a computation, and it
crosses inward.

**The result goes to the screen, not back to the module.** A composer's
contribution is an expression plus the name of a published definition, under
[platform#88](0088-a-contribution-composes-from-published-definitions.md), and
the Platform composes the slot from what it computed. Returning the outcome to
the module would leak by inference what withholding the rows was protecting —
twenty node ids ranked by viewing is a statement about viewing.

**A composer therefore holds no read grant over the library or over history.**
There is nothing to declare and nothing to consent to, because nothing is handed
over.

### Identity providers

**An identity provider attests; it does not authenticate.** It verifies a
credential against its own upstream and returns the assertion "this is *that*
identity at *that* provider". The Platform decides what, if anything, that means
on this install. The module never issues a session, never sees a Mosaic account,
and never decides authority — the same division
[platform#94](0094-a-gateway-is-invoked-from-outside-and-holds-no-authority.md)
draws for a gateway.

**What the Platform does with an assertion depends on when the provider was
established, and there are exactly two modes.**

**Configured during onboarding: an assertion provisions an account.** At that
moment the install is being defined. There are no accounts to reach, no library
to expose, and the operator is declaring their existing directory to be the
source of truth for who exists here. The onboarding screen states plainly that
this is what it is doing, because it is a standing decision rather than a step.

**Configured after onboarding: an operator links an existing account to an
external identity, explicitly, before that identity can sign in.** Accounts with
grants now exist, and an attestation must not be able to reach one nobody
deliberately connected. A compromised or buggy provider can assert anything and
still reach only what was linked by hand.

**The mode is fixed when the provider is established and cannot be changed
afterwards.** This mirrors
[platform#54](0054-claiming-an-unclaimed-server.md), where claiming is
unauthenticated precisely because there is nobody yet to authenticate against,
and "refuses once any user exists" closes the window permanently. The same seam
is being used here: a foundational trust decision is available while an install
is being defined and never again.

**No assertion ever carries authority.** It says who somebody is, never what they
may do. Roles and grants are Platform state, assigned by a person, and an
auto-provisioned account is no exception — it arrives with whatever the
onboarding decision said and nothing a provider can influence.

## Alternatives considered

**A declared read grant over the library, with the composer deriving its own.**
Anything is expressible, including what nobody anticipated. Rejected: the library
crosses to a third party, which is what keeping `ListLibrary` off the SDK avoided,
and once a module holds it what it does with it is unobservable.

**Restricting composers to a flat filter.** Simpler language, smaller surface.
Rejected as too narrow a reading of its own principle — it would have excluded
scoring and pattern matching for no protective gain, since neither requires the
module to see anything.

**Feeding watch history to module code to score.** The most expressive option and
it needs no expression language at all. Rejected: a module process has egress and
storage, so this is a read grant with a different calling convention.

**Returning the computed result to the composer.** Convenient, and it would let a
module refine its own output. Rejected: the ranking is itself a statement about
viewing, so it leaks by inference exactly what withholding the rows protected.

**An identity provider that authenticates outright.** Simplest, and any protocol
becomes possible without the Platform modelling it. Rejected: identity would stop
being Platform-owned in fact while remaining so on paper, and a module bug would
be an authentication bypass.

**Auto-provisioning at any time.** What people expect of single sign-on, and the
reason they want it. Rejected as a standing capability: after onboarding, an
external assertion creating principals in a populated install is an escalation
path that no amount of default-role care closes. It is allowed only in the window
where there is nothing yet to escalate into.

**No identity providers at all.** The invariant is untouched. Rejected: every
protocol anybody wants becomes Platform work, and a self-hoster with an existing
directory has no path.

## Consequences

- **The expression language is a closed set somebody maintains**, and it is the
  second such vocabulary this milestone has created after "what a module needs"
  in [platform#87](0087-module-lifecycle-events-progress-and-schedules.md). A
  composer whose algorithm the language cannot state has no route, and that is a
  Platform finding rather than the author's problem.
- **A genuinely novel derivation may be inexpressible.** Anything resembling a
  learned model is not a declarative expression, and this decision has no answer
  for one.
- **Two provisioning modes exist and an install can never switch.** Somebody who
  wanted auto-provisioning and did not configure it during onboarding cannot have
  it, and the only remedy is a new install — which is a sharp edge, chosen over
  a reconfiguration path that would reopen the window the decision closes.
- **"During onboarding" needs a boundary sharp enough to enforce**, and
  [platform#54](0054-claiming-an-unclaimed-server.md)'s "refuses once any user
  exists" is the shape to follow rather than a wall-clock window or a wizard step
  somebody can return to.
- **An auto-provisioned install trusts its directory completely for who exists.**
  That is the decision the operator made, and it should be visible afterwards
  rather than only at the moment it was taken.
