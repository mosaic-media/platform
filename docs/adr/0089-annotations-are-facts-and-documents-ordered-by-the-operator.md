# Annotations are facts and documents, resolved by group in an order the operator sets

**Status:** Accepted. **Not built.** Depends on
[platform#85](0085-a-modules-authority-is-declared-and-consented.md). Slice 5 of
the extension surface.

## Context

Only a node's own source can say anything about it. `Node.Attributes` is opaque
JSONB written wholesale when the node is created, with no provenance and no
layering, so a second module cannot add a fact without replacing one.

Modules already annotate informally inside that constraint — `module-tmdb` writes
its own `tmdbWatch` document into `Attributes` at import. What is missing is not
the idea but the shape: somewhere to put a second opinion, an order for resolving
two of them, and a rule keeping any of it away from decisions it must not make.

Two existing mechanisms decide most of this, and both are worth reading before
inventing anything.

`storeAvailability` states the containment rule already: the facet is written
from `ContentMetadata.Watch`, the typed value the SDK carries, "and never from a
module's attributes document. That is what keeps the facet free of any module's
vocabulary." It also deliberately lets an empty answer overwrite a positive one,
because a title that has left every service must stop matching the facet and
"skipping the write would freeze the last positive answer forever."

`module-stremio-addons` has already solved the merge, in the small. Sources are
sorted by the order a user configured them in, and the merge then resolves **by
coherent group rather than by field**: identity whole from the first source that
has one, artwork as a set from the first that has any, supplementary lists
unioned across all in priority order. The reason is stated at the point of it —
"a title from one source and an overview from another is the blend this tier
exists to prevent."

## Decision

**An annotation is a fact or a document, and it declares which.** A *fact* is a
typed key and value with provenance: one row, queryable, comparable against
another annotator's answer for the same key. A *document* is an opaque payload
per node, module and kind: fetched rather than queried, and never resolved
field by field. The two motivating cases are three orders of magnitude apart — a
filler flag is one bit and a read-along sync map is around a hundred thousand
triples — and one shape serving both would either make the bulk case unusable or
make the small case unresolvable.

**Precedence is an order the operator sets over modules, at install.** This is
the pattern the audience already knows: a Stremio user orders their addons and
understands that the order is the priority, and
`module-stremio-addons` already implements exactly this internally over the
addons a user configured. Lifting it to the Platform makes one familiar rule
govern both levels rather than having a module-local convention and a different
Platform one.

**The user sits above every module in that order, unconditionally.**

**Resolution is per declared group, not per key.** An annotation declares the
group it belongs to, and a group resolves whole from the highest-precedence
annotator that has any of it. Per-key resolution would reintroduce precisely the
blend `module-stremio-addons` sorts its sources to avoid — a value from one
annotator beside a related value from another, each individually correct and
jointly describing nothing. A fact with no group is its own group, which is the
filler flag's case and needs no ceremony.

**A user's annotation is never overwritten by a module's, empty or otherwise.**
That is what highest-precedence has to mean, and surviving the next enrichment
pass is the whole reason the rule exists. This does not collide with
`storeAvailability`: availability is a typed Platform field written from a typed
SDK value, not an annotation, and its empty-overwrites rule stays exactly as it
is. The two rules govern different things and neither moves.

**The Platform may sort and filter on annotations. It may never authorize on
them.** This is a deliberate widening of "annotations inform; only
Platform-validated fields decide", recorded as a widening rather than slipped in.
A module influencing what a person *sees* is what a source already does; a module
influencing what a person is *allowed* to see is the thing that must never
happen. The boundary is authorization, not Platform behaviour in general.

**No annotation may carry an authorization input, under any of the above.** Not
an age rating, not a gating content warning, not a "safe" flag. A module
supplying a rating that decides access is a module unlocking parental controls.
This is [platform#88](0088-a-contribution-composes-from-published-definitions.md)'s
principle applied to data rather than to a surface: no mechanism may offer a
route around the permission system.

**Precedence is the operator's; placement is the viewer's.** Slice 4 gave row
arrangement to each person
([platform#59](0059-one-library-many-viewers.md)) while this gives annotation
precedence to the operator, and the asymmetry is deliberate: placement is taste
and may differ per person, while precedence is a claim about which answer is
true, and an install answering differently per viewer would mean the library
disagreed with itself.

## Alternatives considered

**Facts only.** One shape, queryable, comparable. Rejected because a sync map
becomes ~100k rows per title, which makes the bulk case a reason to avoid the
mechanism rather than use it.

**Documents only.** Closest to what modules already do informally. Rejected
because nothing is queryable and precedence can only be per document, so two
modules disagreeing about one field cannot be resolved — the case that motivated
the slice.

**Flat precedence below the user, ties by recency.** Honest about what provenance
can express, and it invents no ranking the data cannot support. Rejected because
an operator who trusts one source over another has no way to say so, and last
writer wins is a race rather than a policy.

**Precedence by the node's own source provider.** Intuitive, and rejected on a
fact already recorded:
[platform#85](0085-a-modules-authority-is-declared-and-consented.md) notes that
provenance says which module's invocation wrote a binding, not which module is
authoritative for a node, because several may bind the same node under the same
scheme.

**Annotations inert to all Platform behaviour.** The strictest reading of the
original line. Rejected: it pushes "show me everything flagged filler" onto every
client separately, which is a real capability lost to a boundary drawn wider than
the danger.

## Consequences

- **Precedence is a thing an operator must curate**, and most will not. The
  default order therefore decides almost everything in practice, and what that
  default is deserves its own attention rather than being install order by
  accident.
- **A group is a new declared concept a module author must understand.** Getting
  it wrong produces the blend the grouping exists to prevent, and nothing will
  report it — the result will simply read slightly wrong.
- **Sorting and filtering on annotations makes a module able to move a title up
  or down a listing.** That is a soft influence on attention, granted
  deliberately, and it is worth remembering when the first complaint about a
  module's placement arrives.
- **Two storage shapes and two lifecycles** exist from the outset, including the
  question of what removes a document when the module that wrote it is
  uninstalled — which annotations, unlike library rules, have no answer for yet.
