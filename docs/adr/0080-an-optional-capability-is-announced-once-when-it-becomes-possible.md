# An optional capability is announced once, when it becomes possible

**Status:** Accepted. Nothing is built. **Correction, same day:** the Alternatives section rejects a modal partly on the claim that the contract has no overlay mechanism and that adding one would be a client release. **That claim is wrong.** `OpenOverlay` and `CloseOverlay` are declared action kinds, `modal`/`sheet`/`drawer` are declared surfaces, and the extensions screen already uses them (`ui.Overlay(ui.SurfaceModal, installOverlay(e))`, `internal/transport/screens/settings.go:745`). The mistake was looking for a *component* named Modal and never checking the action and surface tiers of the vocabulary. The decision — a banner — therefore stands on the interruption argument alone, which is the weaker half of what the body claims, and is worth revisiting on those terms rather than on cost. The rule is stated generally because Mosaic already has several capabilities only some installs can have, but the two it governs today are passkeys ([platform#78](0078-passkeys-are-an-optional-layer-on-a-public-origin.md)) and TOTP ([platform#79](0079-totp-is-the-second-factor-that-works-everywhere.md)).
**Date:** 2026-08-10

## Context

[platform#78](0078-passkeys-are-an-optional-layer-on-a-public-origin.md) made
passkeys available only to installs with a public origin, and
[platform#79](0079-totp-is-the-second-factor-that-works-everywhere.md) made TOTP
available to all of them. Both are optional, and neither decided **when a user is
told the option exists** — which turns out to be most of whether either gets
adopted, and all of whether they irritate.

The failure modes are specific and both are common in software of this shape:

- **Announcing something an install cannot have.** A `.local` Mosaic that
  mentions passkeys is describing a feature that will fail if anyone tries it.
  [platform#36](0036-telemetry-storage-retention-and-expert-mode.md) already met
  this with the expert-mode toggle and corrected it the same way: the record had
  the toggle visible to everyone and the data denied, which means routinely
  showing people a control that fails.
- **Announcing repeatedly.** A prompt that returns every session is the pattern
  that trains people to dismiss without reading, and it is what makes a security
  feature feel like an upsell.

There is a third, quieter one that a naive fix creates: **announce once, never
again, and provide no other route** leaves someone who dismissed it on day one
with no way back except knowing to go looking.

## Decision

**An optional capability is silent until the install can actually offer it,
announced exactly once at that moment, and permanently available afterwards
without ever being announced again.**

- **Silence until possible.** If this install cannot support it, nothing
  mentions it: no greyed control, no "unavailable" row, no documentation link in
  the interface. A user of a LAN-only Mosaic should not learn that passkeys exist
  by being shown a thing they cannot use.
- **Announced at the moment it becomes possible, and to the person who can act.**
  For TOTP that is during onboarding, because it works from day one. For passkeys
  it is the superuser's first sign-in *via the public origin*, because that is
  the first moment the capability exists.
- **Exactly once. Enable now, or later.** Both answers end the announcement
  permanently. Declining is a legitimate choice and is not re-litigated.
- **The settings row is permanent once the capability exists**, whatever was
  answered. This is what keeps "once" from becoming a dead end: the option is
  always findable, it is simply never again *raised*.
- **Detecting is not setting.** [platform#78](0078-passkeys-are-an-optional-layer-on-a-public-origin.md) refuses to infer the relying-party id
  from a request's `Host` header, and that stands. Noticing that a request has
  arrived on a public-looking origin is a fine reason to *ask*; the answer is
  what writes the configuration explicitly. The distinction is worth stating
  because the two look similar enough that a later simplification could collapse
  them, and collapsing them reintroduces exactly the silent failure [platform#78](0078-passkeys-are-an-optional-layer-on-a-public-origin.md)
  rejected.
- **What counts as a public origin is what a browser accepted**: not an IP, not
  `.local`, and served over HTTPS with a certificate the browser did not warn
  about. That last clause is the operative test — a name that looks public but
  whose certificate the user clicked through is not an origin WebAuthn will
  work on.
- **It is a banner with actions, not a modal.** The contract has no `Modal`,
  `Dialog` or overlay primitive, so a true popup is a **vocabulary** change —
  a new primitive, an ADR, a `@mosaic-media/sdui-react` release
  ([contracts#2](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0002-primitives-and-definitions.md)). A `Stack` holding a `Banner`
  and two `Button`s expresses this today at no cost, and interrupts less. If a
  modal is ever genuinely needed it should be decided on its own merits and not
  arrive as a side effect of a security prompt.

## Alternatives

**Nag until enabled.** *Rejected.* It converts a security feature into an
advertisement, and the reliable outcome is people who dismiss prompts without
reading them — which is worse than the thing being prompted for.

**Show a permanently disabled control with an explanation.** *Rejected.* It is
honest and it is the shape most software picks, and it still teaches every
LAN-only owner about a feature they cannot have, on every visit to that screen.
Silence carries the same information at no cost.

**Settings only, no announcement at all.** *Rejected*, and it is what would
happen by default if nothing decided otherwise. A second factor nobody is told
about is a second factor nobody adds; the whole value of the onboarding step for
TOTP is that it is the one moment a person is already deciding how their server
is set up.

**Announce once and drop the settings row when declined**, honouring "no more
alerts" literally. *Rejected.* It makes a single dismissal irreversible for
anyone who does not know the feature exists, which is the population that
dismissed it.

**A modal, so it cannot be missed.** *Rejected on cost.* It needs a primitive
Mosaic does not have and every client would have to implement, in service of an
interruption that a banner delivers. Reconsider it when something genuinely
blocking needs one.

## Consequences

- **Two capabilities now have a stated announcement moment**, and any third
  optional capability has a rule to follow rather than a fresh argument to have.
- **The install has to know whether it can offer a capability**, which for
  passkeys means the origin check is a real piece of behaviour rather than a
  configuration read — and one whose answer changes over an install's life.
- **"Announced once" is state that has to be stored per install and per user**,
  and it must survive a restart, or the guarantee is not one.
- **A user who moves their Mosaic to a public origin long after setup still gets
  the announcement**, because the trigger is the capability becoming possible
  rather than a moment in the calendar. That is the intended behaviour and it is
  the reason the trigger is not "first sign-in".
- **This says nothing about capabilities a user lacks *permission* for**, which
  is a different question with an existing answer ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md)'s rule: an
  affordance nobody may use is not drawn). The two rules agree in effect and are
  reached from different directions.
