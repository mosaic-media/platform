# TOTP is the second factor that works everywhere

**Status:** Proposed. Nothing is built. `domain.RecoveryFactor` already exists — single-use, hashed, with `ConsumedAt` — and has never had a caller; this is what gives it one.
**Date:** 2026-08-10

Extends [platform#43](0043-one-principal-many-credentials.md), which named four
credential kinds and did not name this one. It supersedes nothing, and the
distinction in the first bullet below is why: TOTP is not a fifth arrow into
that record's Principal constructor.

## Context

[platform#78](0078-passkeys-are-an-optional-layer-on-a-public-origin.md) settled
that passkeys are an optional layer available only to installs with a public
origin, and that username and password remains the mandatory foundation. That
leaves a real gap it named and did not fill: **an install reached at
`192.168.1.50` or `mosaic.local` has exactly one secret between an attacker and
every account on it**, and no path to a second, because WebAuthn cannot run
there at all.

That is the majority deployment for a home media server, and it is the one where
the owner is least likely to be thinking about credential hygiene.

TOTP ([RFC 6238](https://www.rfc-editor.org/rfc/rfc6238)) is the six-digit code
that rolls over every thirty seconds. The server and an authenticator app share
a secret at enrolment, and both compute `HMAC(secret, current 30-second window)`
truncated to six digits. **It needs no origin, no domain, no certificate and no
network** — which is precisely the property passkeys lack and the reason it fits
where they do not.

Its weaknesses are equally worth stating, because they are why it does not
replace passkeys:

- **It is phishable.** A fake sign-in page can relay a code in real time. A
  passkey cannot be relayed, because the browser refuses to release it to the
  wrong origin.
- **The secret is shared.** The server holds the same value the phone does, so a
  database breach exposes the factor. A passkey's private half never leaves the
  authenticator.
- **It depends on a roughly correct clock** at both ends.

## Decision

**TOTP is an optional second factor on the password path, offered during
onboarding and available to every install. It does not replace the password and
does not compete with passkeys.**

- **It is a factor, not a credential, and that is not pedantry — it changes
  where it goes in the code.** [platform#43](0043-one-principal-many-credentials.md)'s diagram has four credentials each
  resolving to a `Principal`; TOTP resolves to nothing on its own, because the
  server holds the same secret and possession of a code proves a device rather
  than an identity. So it does not add a fifth arrow. **It makes the password
  arrow two-step**, and the thing that records the difference is
  `domain.AuthStrength`, which gains a value for a password proven alongside a
  second factor. A session established with both is stronger than one
  established with a password alone, and the policy engine can eventually say so.
- **Enrolment is two-phase: pending, then confirmed by a code.** The secret is
  generated server-side, shown once as a QR and as text, and stored **inactive**
  until the user enters a current code. Storing it active on generation locks out
  anyone whose scan silently failed, which is the standard way this feature
  turns into a support burden.
- **Recovery codes are issued at enrolment and shown exactly once.**
  `domain.RecoveryFactor` already exists for this — single-use, hash-only,
  `ConsumedAt` set on use — and has never had a caller. A second factor without
  a recovery path is a way to lose an account when a phone is lost, and on a
  self-hosted server there is no support desk to appeal to.
- **It is offered during onboarding, as its own optional step.** It works from
  day one, unlike passkeys, so it belongs where the account is made rather than
  behind a later prompt: a factor deferred to settings is a factor nobody adds.
  The wizard behind [platform#54](0054-claiming-an-unclaimed-server.md) is four
  steps today, cut down from the concept's six, so a fifth is a re-expansion
  that has to earn itself — it earns it because this is the one step whose value
  collapses if it is moved later.
- **Skippable, plainly, without a warning that reads as a scolding.** An owner
  setting up a media server on their own network is making a reasonable choice
  by declining, and the settings row stays available afterwards.
- **Per user, never per install.** One person adopting a second factor must not
  require another to. Whether a superuser may *require* it of everyone is a
  different decision about administration and is deliberately not taken here.

## Alternatives

**Do nothing, and treat passkeys as the whole second-factor story.**
*Rejected.* It leaves every LAN-only install on a single secret, which is most
of them, and the exclusion is permanent rather than temporary — WebAuthn will
never run on an IP address.

**TOTP instead of passkeys**, since it works everywhere and one mechanism is
simpler than two. *Rejected.* The users who expose Mosaic to the internet face
credential phishing, and TOTP does not stop it — a relaying page defeats it in
real time. Refusing the unphishable option to the population that most needs it,
because another population cannot use it, is the wrong way round.

**TOTP instead of the password**, which is how the question first arrived.
*Rejected, and it is not a preference:* the server holds the same secret the
authenticator does, so a code is not an identity proof and cannot stand alone.

**Email or SMS codes.** *Rejected.* A self-hosted server has no mail
infrastructure it can rely on and certainly no SMS gateway, and both are weaker
than TOTP against the attacks that matter.

**Offer it after first sign-in rather than during onboarding**, as a prompt like
the passkey one. *Rejected*, and the asymmetry is the point: a passkey *cannot*
be offered at onboarding because the origin is not final, and TOTP can. Deferring
the one thing that works immediately, to match the shape of the thing that
doesn't, would trade adoption for symmetry.

## Consequences

- **The setup wizard grows from four steps to five**, re-expanding a flow that
  was deliberately cut down. The record says why in the same breath rather than
  leaving a later reader to find a wizard longer than anything that explains it.
- **`domain.RecoveryFactor` gets its first caller**, and with it the question of
  what a recovery ceremony actually looks like — which this record does not
  answer beyond "the codes exist and are single-use".
- **A new store and a new domain type**: the shared secret is credential material
  and must be classified accordingly ([platform#34](0034-redaction-classes-are-the-pii-boundary.md)) — never logged, never rendered
  after enrolment, never returned by a read.
- **Sign-in gains a second round trip** for accounts that have enrolled, and the
  doorway gains a state it does not have today.
- **`AuthStrength` becomes load-bearing rather than decorative.** It has only
  ever held one value; once it can hold two meaningfully, policy that reads it is
  a real possibility and its absence becomes a choice.
- **Nothing here helps an account that has neither.** The password remains the
  only thing standing in front of a fresh install, which is why the step is in
  onboarding rather than buried.
