# A module's authority is declared in its manifest and consented at install

**Status:** Accepted. **Not built.** Depends on
[platform#84](0084-authorization-is-scoped-to-the-resource.md). Gates the rest
of the extension surface: verbs, storage, gateways and composers each need
authority a user does not have.

## Context

[platform#13](0013-how-a-capability-acts.md) settled the acting principal: a
capability acts as the user who invoked it, and its writes re-authorise as that
user. That is the right default and it is why a module cannot quietly exceed the
person using it.

It is also a ceiling. A module that scrobbles to a third-party account, writes
to its own storage, binds a gateway path or derives a view from the library is
not acting as its user in any meaningful sense — the user has no such authority
to lend. The
[deliberately-undecided list](https://github.com/mosaic-media/architecture/blob/main/docs/index.md)
defers module-granular permissions to *"the first capability that needs
authority distinct from its user's"*, and four of the extension surface's slices
are that capability.

Three facts about the code shape what is possible:

- **`policy.Subject` has no module identity.** It carries `UserID`,
  `AuthStrength` and `System bool`. `System` is the useful precedent: it is set
  by the enforcement point from *how the caller authenticated*, never from an
  identifier the caller supplies, so it cannot be forged by naming it.
- **The Platform cannot currently tell which module is writing.** A capability
  is handed `s` — the `*app.Service` itself — as its `ContentService`, together
  with the invoking user's `Caller`. Every module receives the same object. The
  nearest thing to a module identity on that path is `moduleSpan(ctx,
  cmd.Ref.Provider, …)`, which is the *provider scheme* and goes to telemetry.
- **`SourceBinding` cannot identify a module either.** It records
  `SourceProvider`, a scheme, and `module-cinemeta` and `module-stremio-addons`
  both bind under `imdb` deliberately, so that a title added by one is the same
  Work as one added by the other rather than a duplicate
  ([platform#18](0018-virtual-and-materialized-content.md)). Provenance by
  scheme therefore cannot distinguish two modules, and must not be used to.

## Decision

**A module declares the authority it needs in its signed manifest, and an
operator consents to it at install.** The manifest is already signed and its
digest already verified before a binary runs
([platform#40](0040-module-distribution-and-trust.md)), so the declaration is
tamper-evident, and the install path already puts a consent step in front of a
human ([platform#51](0051-extension-installation-is-user-initiated-and-persistent.md)).
The trust decision lands where a human is already being asked to trust the code.

**The grant attaches to the module, not to each capability.** The binary is what
is signed, digest-verified and run as one process, and `ModuleSettingsStore`
already holds one document per module. Capability-level grants would be honest
on paper and not in fact: the process boundary is the module, so a compromised
binary holds every capability's grant regardless.

**A module principal is a field on `policy.Subject`, set by the enforcement
point**, exactly as `System` is. It is derived from how the invocation
authenticated and never from an identifier a caller supplies. Module decisions
then run through `policy.Engine` like every other, and appear in the trace
beside them — rather than through a second authorization path outside the one
place that holds authorization rules.

**A module is handed a `ContentService` bound to its identity for that
invocation**, in place of the bare `*app.Service`. This is the same shape as the
per-invocation `Caller` handle a module already receives. It is what makes
attribution unforgeable: the module never holds an unbound service, so it cannot
write as another module, and it cannot drop its identity by choosing which
context to pass. Carrying the identity in the context instead was rejected for
that reason — the module chooses what it passes back, so the identity would be
advisory, which is not a basis for an authorization decision. The SDK's
`ContentService` interface does not change, so no existing module is edited.

**Which module sourced a node is recorded on `SourceBinding`, written only by
the Platform**, from that bound identity. It sits beside `SourceProvider`
because that is where a reader already looks for provenance, and it is distinct
from it because a scheme names an identifier space and a module names code.

**Grants are scoped to resources**, under
[platform#84](0084-authorization-is-scoped-to-the-resource.md). A module holds
an action over what it is entitled to act upon, not over everything of that
kind.

**Actions reuse the existing vocabulary where they mean the same thing, and take
a `module.` prefix where only a module could hold them.** `content.write` is one
action whether a person or a module holds it, and duplicating it would produce
two spellings of one permission. Filesystem reach, LAN discovery and gateway
binding have no meaning for a person, and naming them apart keeps them from
being granted to one by mistake.

**That split is enforced, not conventional.** `domain.Role` is the only path by
which an action becomes grantable to a person, so **role creation refuses a
`module.`-prefixed permission**. No user-grantable role can contain one; module
grants arrive from the manifest rather than through a role and are unaffected.
An unenforced naming convention would have been a rule that holds until somebody
is in a hurry.

**A module update whose manifest asks for more than the installed version is
refused until it is re-consented.** The installed version keeps running on its
existing grants, which is what boot already does with pinned bytes
([platform#51](0051-extension-installation-is-user-initiated-and-persistent.md)).
An operator is shown what the new version wants and chooses. A module never
gains authority nobody approved.

**A grant is revocable without uninstalling.** The module keeps running and its
affected operations fail; a source that has been narrowed degrades visibly, the
way a source that is not answering already earns a standing notice rather than
an empty screen ([platform#30](0030-cache-first-rendering-and-source-health.md)).

**A denied attempt fails with `PermissionDenied` and the module sees it.** It is
one of the seven contract error categories already, the module can handle or
report it, and the denial appears in the trace like every other authorization
decision.

## Alternatives considered

**Never grant a module authority distinct from its user.** Keeps
[platform#13](0013-how-a-capability-acts.md) pure and needs no new mechanism.
Rejected because it does not defer the extension surface, it cancels four slices
of it: verbs acting on a third-party account, module storage, gateways and
composers each require authority no user holds.

**Grant after install, from a permissions screen.** Separates *I trust this
code* from *I allow this action*, and makes a grant revocable independently —
which this record achieves anyway. Rejected as the primary path because it
introduces an installed-but-inert state, where a module is present and silently
unable to work, which a user reads as broken rather than as unauthorised.

**A synthetic user account per module.** Reuses roles, grants and the audit
trail with no new concepts. Rejected because it makes a module a user: the
People panel and every user-listing surface must then filter them out, and a
bug in that filter displays a module as a person.

**Auto-grant an update within an already-approved category.** Less friction on
routine updates. Rejected because it needs a category taxonomy that is correct,
and the value of install-time consent is precisely that a human saw the specific
list.

## Consequences

- **`SourceBinding` gains a field**, so the published SDK takes a minor bump and
  consumers move. The field is Platform-written; a module supplying it would
  defeat the point.
- **Every capability invocation allocates a bound `ContentService`.** Small, and
  the same shape as the `Caller` handle already minted per invocation.
- **Modules must handle losing authority mid-life**, because a grant is
  revocable. A module that assumes its grants are fixed for the life of the
  process will fail confusingly rather than reporting a permission problem.
- **The `module.` prefix is a naming rule with one enforcement point.** It holds
  as long as role creation is the only way an action reaches a person. A second
  path that attaches permissions to a subject would have to carry the same
  refusal, and nothing would notice if it did not.
- **Provenance answers a narrower question than it appears to.** The new field
  says which module's invocation wrote a binding, not which module is
  authoritative for a node — several may bind the same node under the same
  scheme, which is the behaviour
  [platform#18](0018-virtual-and-materialized-content.md) depends on.
