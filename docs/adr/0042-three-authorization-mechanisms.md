# Authorization has three mechanisms, not one

**Status:** Proposed
**Date:** 2026-07-23

## Context

Mosaic has one authorization mechanism: roles granting actions, evaluated by
`policy.Engine`, default-deny. It answers "may this user perform this action?"
and nothing else. `Resource` and `PolicyContext` are accepted and discarded —
the signature is ABAC-shaped, the logic is not.

A concrete household makes the limits of that immediate. One VPS, four accounts:

| | Requirement |
|---|---|
| 1 | Administrator — full access and control |
| 2 | Films and TV, and search. No anime — they do not want it |
| 3 | As user 2, plus anime |
| 4 | A child. Films, TV and anime, age-limited to PG12 |

Only the first is an action question. The other three are all about *which
content*, and the current engine cannot express any of them.

The important part is that **users 2 and 4 look identical in the requirements
and are not the same mechanism.** User 2 does not want anime; if they somehow
opened an anime node, refusing them would be absurd — the requirement is that
it stays out of their lists. User 4 reaching an 18-rated film must be refused,
must fail closed, and must not be reachable by knowing a node id.

Mosaic already has the note that catches this. `BoolPreference` states that
gating a visibility hint behind a permission check "is how a toggle becomes an
accidental access control" ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md)).
The same trap runs both ways: build user 2's filter as a permission and you have
an access control nobody asked for; build user 4's limit as a preference and you
have a child-safety feature that is a UI suggestion.

## Decision

**Three mechanisms, kept separate, because they answer three different questions.**

```
User → Role(s)        → which actions may I perform     (exists, unchanged)
User → ContentScope   → what am I permitted to see      (new)
User → Preferences    → what would I rather not see     (exists, not authorization)
```

- **Roles stay exactly as they are.** RBAC over actions, default-deny. User 1 is
  entirely served by what is built.
- **`ContentScope` is small and attribute-based**: a maximum age rating and a
  media-type set. It is *not* an access-control list. Nothing in these four
  users is "user X may see node Y", so there is no grant table that grows with
  the library and no join on every read. That is the expensive design and it is
  not needed.
- **Preferences are not authorization** and must not be routed through the
  engine. `UserPreferenceStore` already exists; user 2's exclusion is a stored
  preference applied as a query filter.
- **The scope is resolved once per request and carried on `authorized`**
  ([platform#41](0041-authorization-is-carried-in-the-type.md)), alongside the
  resolved user id.
- **The predicate is pushed into the store query. Handlers never post-filter.**
  If `contracts.NodeQuery` requires a scope to construct, an unscoped content
  read is not expressible.
- **An unbounded scope is a value, not an absence.** The zero value means "sees
  nothing", so a path that forgets to set a scope fails closed. Default-deny
  applies to visibility or it is not default-deny.

The engine grows a second question rather than a second meaning for the first:

```go
Authorize(ctx, subject, action, resource, policyCtx) (Decision, error)   // exists
ScopeFor(ctx, subject) (ContentScope, error)                             // new
```

"May I do X?" and "what may I see?" are different questions and a boolean is the
wrong answer to the second.

**Ratings are stored as a numeric minimum age** (0/6/12/15/18), normalised from
whatever certificate a source supplies, with the original string retained.
Otherwise the comparison at query time is "PG-13" against "12" against "FSK 16",
which is unanswerable. Policy compares an integer; a mapping table absorbs the
mess.

**Work-level ratings only, initially.** Per-node ratings make a container's
visibility a function of its children, and the tree walk gets expensive quickly.
Coarser and shipped beats correct and deferred, provided it is stated.

## Why the predicate goes in the query

Three reasons, in order of weight:

1. **No path can be forgotten.** Search, catalog browse, node read, parts list,
   playback resolve, and whatever the admin surface adds — six-plus paths, and
   the one that gets missed is the one that matters. A compiler obligation beats
   a checklist.
2. **Pagination stays honest.** Post-filtering `LIMIT 50` down to 38 gives ragged
   pages and a "load more" that lies about what is left.
3. **No per-node authorize.** Post-filtering means a policy decision per row,
   which is exactly the defect [platform#41](0041-authorization-is-carried-in-the-type.md)
   removed twice.

It also turns out to be the only thing that makes legacy clients safe — see
[platform#43](0043-one-principal-many-credentials.md), where a Jellyfin client that
has no concept of a scope still receives a scoped result because there is no code
path that produces an unscoped one.

## Alternatives considered

**Express content scope as roles.** *Rejected.* Roles are action-scoped, and
reusing them for content produces a combinatorial explosion: four users already
generate three distinct content policies, and "viewer", "viewer-no-anime",
"viewer-pg12", "viewer-pg12-no-anime" is the beginning rather than the end.

**Per-node access-control lists.** *Rejected as unnecessary.* Every requirement
here is attribute-based. An ACL table is the expensive answer to a question
nobody asked, and it would need a join on every content read.

**Filter in the screen builders.** *Rejected.* It puts an access control in a
projection surface, misses every non-screen path (the SDK's `ContentService`,
playback resolution, any future client), and breaks pagination.

**Put the scope in the session token so it need not be resolved.** *Rejected*,
and see [platform#43](0043-one-principal-many-credentials.md) — it makes
authorization stale by design, which for a child-safety control is not a
tradeoff.

## Consequences

- **`policy.Resource` stops being decorative.** It has been threaded through and
  discarded since the first engine, deliberately, so the shape would be real
  before the logic was. This is the change it was left for.
- **Rating provenance is the real obstacle, and it is not an authorization
  problem.** `Node.Attributes` is unvalidated JSONB and
  [platform#9](0009-object-graph.md) assigns its correctness to the writing
  capability — so a rating living there is *asserted by a third-party module*,
  frequently absent, and region-inconsistent. A child's safety cannot rest on
  data the Platform neither controls nor checks. This needs a Platform-owned
  field with an administrator override, and **unrated must mean invisible to a
  restricted account** — which will hide a substantial part of a real library.
  That is a genuine product cost, not a footnote.
- **The discovery plane cannot be filtered on Platform data.**
  `SearchAvailableContent` fans out to source addons and returns *virtual*
  results: no Node, no stored attributes, no rating. Scoping the library is not
  scoping discovery. Either a restricted account does not reach the discovery
  surface at all, or it is filtered on whatever a third party happened to
  return — the first is honest, the second is theatre. This is a separate
  decision and it is not made here.
- **Relations will leak unless scoped too.** The relation-read gap is still open;
  when it closes, an adaptation edge from a permitted work to an excluded one is
  a disclosure. Edges need the same predicate.
- **Playback stays the real gate.** List filtering is UX. If a node id leaks by
  any route, `ResolvePlayback` refusing is what actually protects the account.
  Both, not either.
- **A second forcing function for the `media_types` registry**
  ([platform#11](0011-open-and-closed-vocabularies.md)). "Not anime" over an open,
  canonicalised-but-unregistered vocabulary is string matching. The stakes are
  low here — that is user 2's *preference*, so being slightly wrong is
  survivable — but the pressure comes from a direction [platform#11](0011-open-and-closed-vocabularies.md) did not
  anticipate: it deferred the registry pending a capability introducing a new
  media type, not a user wanting to exclude an existing one.
- **This settles the open question in [platform#41](0041-authorization-is-carried-in-the-type.md).**
  Whether the per-method boundary should become request-scoped was left open
  because the shape of a principal was unknown. A scope has to be resolved for
  every read and cannot be resolved per method call, so the request-scoped
  principal stops being a tidy-up and becomes the enabling change.

## Status of the parts

Nothing in this record is built. Roles and preferences exist and are unchanged;
`ContentScope`, `ScopeFor`, the rating field and the scoped `NodeQuery` do not
exist.
