# Revocation is a signed list, checked on a schedule, and a revoked key is not a yanked version

**Status:** Accepted. **Not built.** Answers the two questions
[platform#51](0051-extension-installation-is-user-initiated-and-persistent.md)
named and declined to invent, which
[platform#40](0040-module-distribution-and-trust.md) opened and
[platform#76](0076-the-signing-key-hierarchy.md) restated as a constraint.
**Extends** platform#76's hierarchy by giving the release key a second job, and
by putting its verifier in the Platform.

## Context

platform#76 states the constraint rather than an answer, which is the useful
half: **revocation needs something an install can reach that is not the thing
being revoked.** Today the only revocation available is to ship a binary with the
key removed and rely on people updating — slow, backwards from what a compromise
needs, and it never reaches an install that does not update.

platform#51 names the other half and leaves it open: an installed extension is
pinned bytes on disk that boot re-adopts, which is the pin working rather than a
gap, and nothing re-examines it afterwards.

The two are one mechanism, and separating them was making both harder.

## Decision

**Revocation is an explicit signed list, published at its own URL, and named by
the index.** Not an expiry on the index. An expiry says *everything is suspect*
and makes a registry outage indistinguishable from a compromise; a list says
*this is revoked*, which is the statement actually being made and the one an
install can act on proportionately.

**It is signed by the release key, not the registry key.** This is what makes the
mechanism satisfy platform#76's own constraint rather than merely appearing to: a
list signed by the key that signs the index could be forged by whoever compromised
that key, so it would be the thing being revoked vouching for its own revocation.
platform#76 keeps the release key offline behind controlled egress and refuses a
root key; using the release key here needs no root and no new custody. The
consequence is that **the Platform gains the release-key verifier**, which it does
not carry today — module trust is the Platform's under
[platform#49](0049-the-platform-manages-extension-modules.md), so the process that
enforces a revocation is the process that must be able to check one.

**The list carries a monotonic sequence number, and an install refuses an older
one than it has seen.** Without it, suppression is trivial: serve yesterday's
empty list and the revocation never arrives. With it, the attack degrades into
unreachability, which is a state the install can see.

**Unreachable is not a verdict.** The install keeps using its cached list and
raises an Issue about the staleness. Failing open silently defeats the mechanism;
failing closed makes every outage look like a compromise and turns a hosting blip
into a household that cannot use its modules. Naming the staleness is the only
answer that does not lie in one direction or the other.

**The check runs on a schedule, and the two causes are handled differently.**

- **A revoked key** is a broken trust decision. The module is stopped and an Issue
  is raised. Its signature no longer means anything, so continuing to run it is
  running unverified code.
- **A yanked version** usually means superseded, not dangerous. The module keeps
  running, an Issue is raised, and the update is offered.

Collapsing those would either strand people whose module was merely replaced, or
keep running something whose signature stopped meaning anything. The distinction
is available because they are different facts in the list, not an inference.

The schedule is what closes platform#51's half: an install that adds no modules
still learns that something changed, which is exactly the population a revocation
has to reach.

## Alternatives considered

**A signed expiry in the index** — platform#76's own cheapest candidate.
*Rejected:* it revokes by making everything stale, so the window has to be short
enough to matter and long enough to survive a registry outage, and those pull
against each other. It also cannot distinguish a compromise from a hosting
failure, which is the distinction an operator most needs.

**Check only at install and upgrade.** *Rejected:* an install that never adds a
module never learns anything changed.

**Sign the list with the registry key.** *Rejected:* it is the key most likely to
be the subject of a revocation, and it is the one CI touches on every release.

**Introduce a root key to sign revocations.** *Rejected:* platform#76 refuses a
root key deliberately, and this record is not a good enough reason to reverse it —
the release key already provides the separation needed.

## Consequences

**An install offline for a long time keeps running a revoked module.** There is no
channel to reach it, so this is a limit rather than a defect, and it is why the
staleness Issue exists — the only honest thing available is to say how old the
answer is.

**The Platform now verifies against two keys**, and the release key's verifier has
to reach it through a Platform release. That is the slow path platform#76 objected
to, applied to the verifier rather than to the revocation itself, which is the
right way round: the key rotates rarely and the list changes often.

**A revocation is a fact somebody has to publish**, so this only works if the
registry treats revoking as a first-class operation rather than deleting a
release. Deleting is how a yanked version currently disappears, and a deleted
release is indistinguishable from a network failure.

**Two Issue types**, since a stopped module and a stale check are different things
a person does different work about.
