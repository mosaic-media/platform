# The browse roles rank their providers; they do not union them

**Status:** Built. `CapabilityRegistry.RegisterFallback` and `fanOutPreferred`
are in the Platform, and the composition root registers `module-cinemeta` as the
fallback tier. Partly supersedes
[module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md): its
"complementary rather than redundant" arrangement stands for what each module
*is*, and is reversed for how the catalog and search fan-outs *combine* them.
**Date:** 2026-07-25

## Context

[module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md)
registered two core modules against one role class — `module-cinemeta` as the
zero-configuration floor, `module-tmdb` as the richer provider for a deployment
holding an API key — and called them "complementary rather than redundant",
exercising the "one or more" arity
[platform#38](0038-platform-binary-built-by-ci.md)'s role-class table already gave.

That record also wrote down the cost, in a sentence about a *different* module:
"the Platform unions search providers without cross-provider dedup, so a
deployment running both would show every title twice." It was the argument for
removing the bundled Cinemeta default from `module-stremio-addons`. It applies
just as exactly to the two core modules the same record registered, and nothing
noticed.

The result was visible on the first home screen a keyed deployment drew: a
Cinemeta "Popular Films" row above a TMDB "Popular Films" row, the same films
under the same name from two sources. Which of the two led was decided by
`sortedIDs()`, an alphabetical sort, so `cinemeta` preceded `tmdb` and the
credential-free floor outranked the provider the user had configured. The
composition root said so plainly: *"'cinemeta' ahead of 'tmdb' is an accident
rather than a policy — which provider wins for a given field is an open seam
neither ordering answers."*

Three separate things had been collapsed into one:

- **Which providers fill a role class.** Both do. That is [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md) and it is
  right.
- **Whether a fan-out over that class is a union.** It was, everywhere, because
  `fanOut` was written for search and then reused for catalogs.
- **Which source a user should see when both can answer.** Nothing decided this.
  Alphabetical order did.

The union is right for *some* roles and wrong for others, and the difference is
not about trust. Searching more places is more found, and a duplicate hit is a
ranking problem. A **catalog** list is a set of named rows on a home screen;
unioning two general metadata sources produces two rows of the same films under
two names, which reads as a bug rather than as breadth.

## Decision

**A read role is either a union role or a ranked role, and the browse roles —
catalog and search — are ranked. A provider registered as the fallback tier is
consulted only when the ordinary providers between them returned nothing.**

Four things follow.

**The tier is declared at composition, not by the module.** `moduleDescriptor`
carries a `fallback` flag and the composition root sets it, which keeps it a
static-composition decision ([platform#4](0004-static-go-module-composition.md)). A module
asserting primacy over its peers would be a claim no module is in a position to
make: it cannot see the others, and a third-party module could assert it about
itself.

**The trigger is an empty answer, not an error.** This is the case that
matters, and an error-keyed fallback would have missed it: TMDB without an API
key does not fail, it answers emptily. A fallback that fired only on errors
would never have fired on the installs the guarantee clause exists for. A
*fatal* error in the preferred tier aborts without consulting the floor — the
query failed, and the floor is for a source with nothing to say, not for one
whose settings could not be read.

**The guarantee clause is untouched.** An install with no TMDB key sees exactly
what it saw before, because TMDB contributes nothing and everything falls
through. [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md)'s requirement — "a fresh install that works" — is what the
fallback tier now names explicitly rather than achieving by alphabetical
accident.

**Metadata is not ranked, because it is not a fan-out.** `PreviewContent`
resolves the provider from `ref.Provider`: a ref carries the module that
produced it, so a TMDB catalog item's detail, artwork and backdrop already come
from TMDB. Ranking the browse roles therefore settles artwork provenance as a
consequence rather than as a second mechanism, and provenance stays answerable —
the property [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md) refused to give up when it rejected putting the fallback
inside `module-tmdb`.

## Alternatives considered

**Leave the union and dedup across providers.** *Rejected*, though it is the
change [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md)'s sentence gestures at. Cross-provider dedup needs a shared
identity, and the two modules do not have one: Cinemeta binds under `imdb`, TMDB
under its own scheme, and [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md) records IMDb-keyed dedup as "a gap
`module-tmdb` has and cannot cheaply close". It would also not fix catalogs,
where the duplication is of *rows* — "Popular Films" twice — not of items.

**A general numeric priority on every provider.** *Rejected.* One concept
instead of a scheme. Two tiers answer the question actually being asked, and a
priority number invites per-deployment tuning of something that should be a
property of the composition. A third tier is a decision, and this record is
where the argument for it would go.

**Rank by role, in the module manifest.** *Rejected* for the same reason as
above: a module would be declaring its standing relative to modules it cannot
see.

**Drop `module-cinemeta` from the binary.** *Rejected*, and it would reverse
[module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md) outright rather than refine it. The floor is what makes a fresh install
work with no key, which is the whole of that record.

**Make it a user setting — "preferred metadata source".** *Deferred, not
rejected.* It is a reasonable thing to want, and the tier here is the mechanism
it would be built on. It needs a place to live that is neither operator config
nor module settings ([platform#17](0017-module-settings.md)), and
inventing one for a preference nobody has asked for yet is the wrong order.

## Consequences

- A keyed deployment's home screen shows TMDB's rows only. This is a visible
  behaviour change for anyone who had both working, and the rows will be
  differently named — TMDB's catalog set is the richer one (eight built-ins plus
  user-defined discover queries) so nothing is lost, but nothing is in the same
  place either.
- Search results from Cinemeta disappear for a keyed deployment. That is the
  intent — it is the duplicate-titles case — but it means a title TMDB does not
  carry and Cinemeta does is no longer findable there. This is the sharpest edge
  of the decision and the most likely thing to want revisiting.
- `fanOut` and `fanOutPreferred` now sit beside each other, and choosing wrongly
  between them is silent. The distinction is written at `fanOutPreferred`.
- The extension tier is unaffected: an installed extension registers ordinarily
  and still unions, which is what a user who chose to install it should get.
