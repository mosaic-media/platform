# A person is an attribute, not a node

**Status:** Accepted. **Not built** — there is nothing to build, which is the
point. Settles the design question `module-tmdb`'s README parks as the reason its
person endpoints are absent. Applies
[platform#101](0101-precedence-is-ordered-by-the-operator-and-dedup-is-exact.md)'s
rule about identity to a second subject.

## Context

A cast chip that opens *more from this actor* needs a person to live somewhere.
`module-tmdb` records this as "a design question rather than an endpoint" and
leaves its person endpoints unimplemented on that basis, which is the correct
order — the endpoints are trivial and the model question is not.

Today a person is a field, not an entity. The contract's `Person` carries a name,
an optional role and a headshot URL, and **no identifier at all.** Sources differ
sharply in what they supply: an addon with only names is common, a real database
gives a character name and an image.

M7 tested the object graph against six hypothetical modules and four reference
clients, and it absorbed anime, music, audiobooks, games and virtual channels
**without a new shape.** That is the property worth defending, and every proposal
to add a node kind is measured against it.

## Decision

**A person stays an attribute of the work that credits them. There is no person
node kind, no person identity, and no person page.** *More from this actor* is a
search by name, over the library and over whatever a source can look up by name.

The reason is the one platform#101 has just been through. A person node would need
an identity that survives across sources, and there isn't one: `Person` has no id,
no two sources agree on person ids, and names collide. So a person node kind is
not one decision — it is a request to invent exactly the fuzzy cross-source
identity platform#101 refused for Parts, where a false merge is worse than a
missed one. Two actors sharing a name merged into one entity is a wrong answer
presented as a fact, and it appears on a page built to look authoritative.

The graph's shape holding through six module classes is not a coincidence to spend
on the first feature that would like an entity. A person page is genuinely nicer
than a name search; it is not nicer than a content model that keeps working.

## Alternatives considered

**A person node kind with cross-source identity resolution.** *Rejected:* it
inherits an unsolved problem and creates a surface where its failures are most
visible. It is also the largest single addition anyone has proposed to the object
graph, for one affordance.

**A person node kind keyed on one source's ids** — TMDB's, say. *Rejected:* it
makes the graph depend on one provider being installed, which is exactly what the
provider-agnostic model exists to avoid, and it is unavailable to a deployment
running only the zero-configuration floor.

**Name-keyed person nodes, accepting collisions.** *Rejected:* it is fuzzy
identity with a table behind it, which makes the wrong answer durable and
queryable instead of merely displayed.

## Consequences

**"More from this actor" is only as good as a name search.** It finds what the
library holds and what a source can look up by name, and it will silently conflate
two people with the same name in its results — visibly, as a list, rather than
authoritatively, as a page. That is the trade, and it is stated so nobody
re-derives it as a defect.

**Cast headshots and character names stay per-work**, so the same actor's photo is
whatever each source gave for that title, and two works can disagree. Harmless,
and it will look like a bug to somebody eventually.

**This is a decision to revisit if a person identifier ever becomes real** — a
cross-source person id, agreed and published, is the thing that would change the
answer rather than a stronger desire for the feature.
