# One library, many viewers

**Status:** Accepted and built. **Watch history landed in M1**, as a
Platform query deliberately kept off the SDK's `ContentService` — no module
needs to read a person's viewing back, and the one list this record is most
emphatic is private should not sit on the surface every installed extension
holds. **Home composition landed in M2.6–2.8** (roadmap M2.8), as a preference
holding the decisions a viewer made rather than a picture of their screen. The
per-user content scope this record sets aside remains unbuilt, deliberately.
**Answers the multi-user
visibility question [platform#26](0026-playback-state-is-platform-owned.md) opens
and does not resolve.** Applies
[platform#42](0042-three-authorization-mechanisms.md)'s separation of roles, scope
and preference to the browse surfaces.
**Date:** 2026-07-26

## Context

Playback state was the first content state that differs between two users of one
install, and [platform#26](0026-playback-state-is-platform-owned.md) said so while
leaving the question open: *whether a household shares a continue-watching rail
is a real question this record does not answer.*

Everything else on a browse surface is currently install-global. Home renders the
same catalogs, in the same order, for whoever is looking, and only the
continue-watching rail differs — and only because playback state happens to be
keyed by user. Nothing was decided; one thing was keyed per user and the rest
were not.

[platform#42](0042-three-authorization-mechanisms.md) already separates the three
mechanisms that could answer this and warns that building one as another is a
defect in opposite directions: **roles** for what a person may do,
a **content scope** for what they may see, and **preferences** for taste. It
does not say which of the three governs what a second account finds on its home
screen.

## Decision

**The library is one shared object graph. Everything about how a person
experiences it is theirs alone.**

**Shared, and deliberately singular:** the materialised content graph — works,
seasons, episodes, Parts, source bindings and artwork. One library per install,
no per-user copy, no per-user materialisation. This is what makes a household
server cheaper than four servers, and it is what the library rules of
[platform#60](0060-the-library-is-built-from-rules.md) maintain: one set of rules
producing one library.

**Private, per user:** playback position and finished state (already true),
watch history, and **home composition** — which rows appear, in what order, and
which are hidden.

Four things follow.

**Home composition is a preference, not a scope.** Hiding a row is taste. A row
a user hides stays reachable by search, by deep link and by every other surface,
and nothing about hiding it is an access control. Anything that genuinely must
not be reachable is a content scope, which stays unbuilt and out of this record —
[platform#42](0042-three-authorization-mechanisms.md)'s child-account case is
untouched here.

**The default composition is the server's, and expressing no preference means
taking it.** A user's stored preference records the decisions they made, never a
snapshot of the whole screen. A newly available row therefore appears for
everybody who has not decided about it. The alternative — freezing each user's
home at the shape it had when they first touched it — is the trap role presets
already fell into, where an account created before an action existed never gains
it.

**Capability omission composes ahead of preference.** An affordance the caller
could not exercise is not rendered at all
([platform#24](0024-capability-gated-affordances.md)); preference then chooses among
what remains. A user cannot un-hide something they were never able to use, and a
hidden row is not evidence of a permission.

**Watch history is per user and is not a shared feed.** It is the same key as
playback state — (user, node) — and it is the record that makes "what have I
watched" answerable without asking what anybody else did.

## Alternatives considered

**Per-user libraries.** *Rejected.* Four accounts on one box is the
specification. Four copies of the graph makes every enrichment pass, artwork
fetch, probe and Part four times the work for a household that is by definition
sharing, and it turns one library rule into four reconciliations.

**A shared continue-watching rail.** *Rejected outright.* A household's viewing
is the least shareable thing on the screen, and a rail that offers to resume
somebody else's episode at somebody else's position is wrong in a way that is
immediately obvious to everyone using it.

**Home composition as a content scope.** *Rejected.* It would put taste in the
policy engine, which [platform#42](0042-three-authorization-mechanisms.md) forbids
for a stated reason: one adult's exclusion is a preference and a child's limit is
an access control, and building either as the other fails in opposite
directions — a preference that gates is unenforceable, and a gate that is a
preference is a security hole.

**Leave home install-global and let each user only have their own rail.** *Rejected.*
It is the status quo by accident rather than by decision, and it makes the
product's most-used screen the one thing nobody can adjust.

## Consequences

- Home is rendered per caller and cannot be cached across users. The emit-side
  already renders per caller for capability reasons, so this adds a preference
  read rather than a new axis.
- The per-user preference document must be readable in the same pass that builds
  home, or the screen costs a round trip per row.
- Every browse surface added later inherits the rule: shared content, private
  arrangement. That includes the streaming-service grouping the register holds
  open, and the Library screen itself.
- A user preference store already exists — it went in for the expert-mode
  toggle — and this is its first content-facing use. It must stay out of the
  policy engine, which is the condition [platform#42](0042-three-authorization-mechanisms.md)
  attaches to it.
- The child account still needs the content scope. Nothing here makes it closer
  or further away; it is deliberately a different mechanism.
