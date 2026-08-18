# A contribution composes from published definitions, on a surface the Platform frames

**Status:** Accepted. **Not built.** Depends on
[platform#86](0086-a-module-verb-is-declared-and-dispatched-by-name.md). Slice 4
of the extension surface.

## Context

A module can source content and, since
[sdk#4](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0004-module-contributed-settings-ui.md),
author its own settings screen. It cannot put anything on a content screen. This
decides what a module may contribute to four named slots — `home.rails`,
`library.sections`, `discovery.rows`, `detail.facts` — and in what form.

Three tiers were proposed: a module supplying **data**, supplying
**presentation**, or drawing its own **tree**. Two things about that framing need
correcting before the decision makes sense.

**Tier 3 already ships.** `SettingsUIResponse` carries the settings screen as a
serialised `UINode` tree. A module already draws its own tree; what has bounded
it is *where* it may do so, not whether it may.

**The stated reason for keeping tier 3 rare was wrong.** It has been recorded
that a module drawing its own tree "stops inheriting the skin, the focus work and
every later improvement". The skin half is false: primitives consume design
tokens directly as CSS custom properties, so a tree of primitives is themed and
re-skinned exactly like anything else, with no client release. The improvement
half is true but narrow — a hand-built card does not inherit a *definition's*
later refinement, which is a maintenance cost rather than a safety one.

The real reason is neither. **`TextInput`, `SelectInput`, `SearchBar`, `Switch`
and `Slider` are native primitives.** A free tree on a surface the user did not
navigate to for that module can therefore draw a credential prompt — a line of
text claiming a session expired, an input, and a control whose action is an
`invoke` on the module's own verb. Every server-side gate holds. The module is
granted nothing it did not have. The user hands it over instead.

## Decision

**A contribution to a content slot is data plus the name of a definition already
published in the contract.** The module composes as a Platform screen composes:
naming published components and supplying their props. It authors no new
presentation, and a module needing a component that does not exist produces a
finding against the contract — the same answer a Platform screen gets when the
vocabulary cannot express it.

**The bound on a free tree is the surface, not the tier.** A module may draw its
own tree where the user navigated to that module for that purpose, which today is
its settings screen. An input field there has honest context: the person went
looking for it. On a borrowed surface the context is stolen, and that is the
whole of the difference.

**Should a free tree ever be wanted on a content slot, the conditions are named
now rather than argued then:** no input primitive may appear in it, and the
Platform draws attribution the module cannot suppress. Without both, a
contribution can impersonate the Platform.

**Placement is the viewer's, through the mechanism that already orders their
rows** ([platform#59](0059-one-library-many-viewers.md)). A contributed rail is
another row a person can hide or move, including the rule that a row nobody has
decided about still appears. A module does not declare a position: two modules
both declaring "first" makes the Platform arbitrate an argument that belongs to
the person whose screen it is.

**A slow or absent contributor behaves exactly as a slow or absent source**
([platform#30](0030-cache-first-rendering-and-source-health.md)): the slot renders
cache-first from the last thing that module said, and a contributor that is not
answering earns the standing notice rather than vanishing. A rail that disappears
is indistinguishable from one never installed.

**The slot set is closed** — those four — and growing it is a decision with a
record. Each slot can then be designed for what belongs in it rather than being
generic.

**A contribution may carry only two actions: `navigate`, and an `invoke` naming
one of that module's own verbs** under
[platform#86](0086-a-module-verb-is-declared-and-dispatched-by-name.md). Not a
Platform action, and not another module's verb. Otherwise a rail is a way to
borrow authority the module was never granted.

**The governing principle, stated because it decided more than one clause above:
no surface may offer a route around the permission system.** A gate that holds
technically and is bypassed socially has not held. Where a design cannot be made
safe against that, the answer is to withhold the surface rather than to document
the hazard.

## Alternatives considered

**Data only, with the Platform choosing all presentation.** The strongest
guarantee, and a contribution would inherit every later improvement permanently.
Rejected because the Platform would have to anticipate every shape a contribution
might take, and a module with a genuinely novel one would have no route at all —
which is the constraint this milestone exists to remove.

**A free `UINode` tree on content slots, as settings already allows.** Maximum
expressiveness on a mechanism that already ships. Rejected on the impersonation
argument above, not on styling: the tree would be themed correctly and would
still be able to ask for a password.

**A module-declared position or priority.** Rejected: it takes the arrangement
away from the viewer, and it makes the Platform referee competing claims to the
top of a screen.

**An open slot set, where any screen declares slots modules fill by name.**
Rejected: a slot name becomes an unversioned contract between a screen and a
module, and renaming a screen's slot would break modules silently.

## Consequences

- **A module's expressiveness is capped by the contract's release cycle**, not
  its own. A contribution needing a component nobody has published waits for a
  contract release, exactly as a Platform screen does.
- **The definition library becomes a shared surface with two consumers.** A
  definition changed for a Platform screen changes every module contribution
  naming it, and there is nothing that would report which.
- **The roadmap's stated reason for keeping tier 3 rare is corrected by this
  record.** Anything else repeating "a module that draws its own tree stops
  inheriting the skin" is wrong and should be fixed where it is found.
- **Attribution is not yet a thing the Platform draws.** The conditions named for
  a future free tree depend on it, so that is unbuilt groundwork rather than an
  existing safeguard.
