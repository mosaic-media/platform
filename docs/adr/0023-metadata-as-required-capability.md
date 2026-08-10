# Metadata is a required capability with a default provider

**Status:** Accepted (the requirement is met; the mechanism differs from the one
specified here). Metadata and search work on first boot with zero configuration,
but the default Cinemeta addon is **bundled inside `module-stremio-addons`**,
opted out with a `disableDefaultAddons` setting and ordered below a user's own
addons — rather than seeded into the Platform's
[platform#17](0017-module-settings.md) settings document at bootstrap as this record
specifies. Whether the default belongs to the Platform or to the module is a
question this record answers one way and the code answers the other; it is
**now resolved by [module-cinemeta#1](https://github.com/mosaic-media/module-cinemeta/blob/main/docs/adr/0001-the-guaranteed-metadata-provider-needs-no-credential.md)**,
which puts the default in a zero-configuration core module and rejects the
bootstrap seed specified here. Also amended by
[architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md), which makes a metadata provider a *core*
module under its guarantee clause.
**Date:** 2026-07-21

## Context

Every Mosaic deployment — remote, local, or both — needs search and metadata
before it can do anything a user recognises as Mosaic: identify content,
enrich it, discover it, dedup it. Metadata is the precondition for the whole
product, not a feature of one deployment shape.

Today the only source of it is the optional Stremio module, and it does nothing
until the user pastes an addon URL into its settings ([platform#17](0017-module-settings.md)).
So a fresh install is **inert** — search returns nothing, every screen is empty
— until it is configured. That reads as broken, not as "not set up yet."

The tempting fix is to promote `mosaic-module-stremio` to the built-in tier
(where Postgres lives — compiled in, required, trusted), so metadata is always
present. That is the wrong tool:

- **Postgres and Stremio are different kinds of thing.** Postgres is the default
  implementation of a *port* — `StorageAdapter`, a swappable infrastructure
  contract. Stremio is a specific third-party *content ecosystem* with its own
  protocol. Promoting it hardcodes the Platform to one ecosystem, which the
  provider-role model ([sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md))
  exists to prevent.
- **Stremio is load-bearing as a proof.** It was built the third-party way — its
  own repo, SDK-only, MIT, tagged versions — precisely because *building it the
  third-party way is what proves the third-party way exists*. Vendoring it into
  `internal/modules/` erases that proof.

## Decision

**Metadata/search is a required *capability class*, not a required *module*. A
functioning deployment must compose at least one provider filling `RoleSearch`
and `RoleMetadata`; which one is a composition choice — the same relationship
storage has to the `StorageAdapter` port (a required contract, a default
implementation, room for others).**

- **The requirement is enforced at the composition boundary — and that boundary
  moves as the Supervisor lands, in two distinct eras that must not be
  conflated:**
  - **Today (no Supervisor): a pre-bundled default.** The composition root
    compiles the Stremio module in and registers it unconditionally, and the
    Platform **seeds a default addon (Cinemeta) into its settings at bootstrap**
    — the [platform#17](0017-module-settings.md) settings document, seeded the way
    [platform#14](0014-first-admin-bootstrap.md) seeds the first admin: idempotent
    and overridable. So the requirement is met with zero configuration on first
    boot. This is the stopgap while the composition root stands in for the build
    pipeline.
  - **With the Supervisor: selected at onboarding, then compiled in
    ([supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md),
    [platform#4](0004-static-go-module-composition.md)).** Module selection happens
    during onboarding; the Supervisor writes the selected modules' dependencies
    into the Platform source and compiles the binary. A module you do **not**
    select is absent from the compiled Platform entirely — not
    compiled-in-and-disabled, just not there. There is **no forced default**: you
    choose a metadata/search provider (Stremio, a TMDB module, any other that
    fills the roles), and the module's own settings (a Stremio addon URL, a TMDB
    key) are gathered during onboarding rather than seeded. **Onboarding will not
    produce a servable binary without at least one metadata + search module
    selected** — the requirement is enforced *before compilation*.
- **Absence is fatal — a serving Mosaic does not boot without it.** This is the
  same stance storage takes: it makes no sense to run Mosaic without a database,
  and it makes no sense to run it without a way to find and describe content. In
  both eras a runtime assertion in the composition root **checks, before the
  serve loop, that the registered capabilities include a provider for
  `RoleSearch` and one for `RoleMetadata`** (one module may fill both) and **fails
  loudly** when either is absent — the same class of fatal startup error as a
  required built-in module failing to register. Under the Supervisor this is a
  backstop (onboarding already refused to build without one); today it is the
  guard behind the hardcoded default. The check binds the *serving composition*
  path, not the `app.Service` constructor, so tests that build a service
  directly are unaffected. There is no first-run path into a loaded-but-useless
  Mosaic in either era.
- **The requirement is role-based, so it is not "internet-mandatory."** What
  satisfies it can be a remote source (Stremio + an addon) or a future
  **local-media** module that provides search and metadata over local content —
  either fills `RoleSearch`/`RoleMetadata`. "Required" is about the capability
  being present, not about which provider or where the data comes from.
- **First-party metadata modules are the path to the richer data, added later as
  ordinary optional modules — not as a replacement for Stremio.** Cinemeta cannot
  provide clearart, banners, franchise collections, or reliable "similar"
  ([sdk#3](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0003-rich-metadata-preview.md)'s recorded gaps). A first-party
  **TMDB** module (its own `MetadataProvider` over the TMDB API) unblocks
  collections, similar and full credits; a **Fanart.tv** module unblocks
  clearart and banners. They augment or supersede Stremio per provider precedence
  (still an open seam), and double as the SDK's second and third consumers.

## Alternatives considered

**Promote Stremio to the built-in (Postgres) tier.** *Rejected.* Conflates a
content source with an infrastructure port, couples the Platform to one
ecosystem's protocol, and erases the external-module proof. The guarantee it
buys (metadata always present) is bought instead by "required capability +
default provider" with none of the cost.

**Hardcode a metadata provider into Core Platform (not even a module).**
*Rejected.* Same coupling, worse — it puts a specific source inside the kernel
and bypasses the provider-role model entirely.

**Leave metadata fully optional and unconfigured (status quo).** *Rejected.* A
fresh install is inert and reads as broken; "paste an addon URL before anything
works" is a hostile first run.

**Bundle Stremio but ship no default addon.** *Rejected.* Bundling without a
seed still requires configuration to be useful — it fixes the packaging but not
the first-run experience.

**Report the missing capability and keep booting (degraded).** *Rejected.* It
recreates the very problem — a user reaching a Mosaic that is loaded but can do
nothing — one soft warning instead of one hard config error. Storage is not
optional-with-a-warning, and metadata/search is the same kind of foundation; the
honest signal is refusing to serve, not serving something inert. Test builds are
unaffected because the check is at the serving-composition boundary, not on the
`app.Service` constructor.

## Consequences

- **First boot always works** — today because the pre-bundled Stremio + seeded
  Cinemeta satisfy the requirement with zero configuration; under the Supervisor
  because onboarding won't produce a binary without a metadata/search module
  selected (and gathers its settings then). Neither era yields an inert install.
- **Stremio stays Tier-3**, so the external-module proof is intact; the "required
  capability, default provider" framing delivers the guarantee without the tier
  violation.
- **A serving instance without a metadata/search provider does not start** — the
  composition root fails fast with a clear message, exactly as it would for a
  missing required built-in module. A composition-time capability check and a
  **bootstrap seed** are the only new machinery — both small.
- **The seed is a default a user owns** — it must be documented and overridable,
  like the admin bootstrap, so a user who wants a different source can remove it.
- **Provider precedence gets more pressing.** Once a deployment can run Stremio +
  a first-party TMDB module, "which provider wins for a given field/role" (an
  open seam) needs answering — but only then, not now.

## Implementation implications

Today, the two pieces are: a bootstrap seed writing a default addon into the
Stremio module's `ModuleSettings` (idempotent, analogous to
`bootstrap.EnsureAdmin`), and a composition-root check that the registered
capability set includes a `RoleSearch` and a `RoleMetadata` provider — returning
a fatal startup error before the serve loop when either is absent, the same
failure class as a missing required built-in module. The check binds the serving
composition, not the `app.Service` constructor, so tests that build a service
directly are unaffected.

Under the Supervisor the *primary* enforcement moves earlier, into onboarding and
the build pipeline ([supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md),
[platform#4](0004-static-go-module-composition.md)): the selection step refuses to
generate a Platform source tree without a metadata/search module, and the
module's settings are collected during onboarding rather than seeded. The
runtime composition-root check remains as a backstop. No tier change and no
promotion — the module system and the composition root already do everything
else. First-party TMDB/Fanart modules are separate, later threads that this ADR
only names.
