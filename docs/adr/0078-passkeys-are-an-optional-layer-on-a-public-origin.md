# Passkeys are an optional layer on a public origin

**Status:** Accepted. Nothing here is built — `PasskeyCredential`, `SavePasskey` and `ListPasskeys` are the whole of what exists, exactly as before. The *announcement* half — silence until an origin exists, one prompt, never again — is [platform#80](0080-an-optional-capability-is-announced-once-when-it-becomes-possible.md). This records the enrolment policy that unblocks building it, and one thing it depends on is untested: whether a browser will run a WebAuthn ceremony on a `.local` origin behind a self-signed certificate.
**Date:** 2026-08-10

Builds on [platform#43](0043-one-principal-many-credentials.md), which decided that
one principal holds many credentials. It supersedes nothing: 0068 said a passkey
is one of four credential kinds, and this says *which installs may enrol one and
when*.

## Context

The roadmap has carried passkeys as blocked on "the domain and origin story",
owed by the owner. That framing turned out to be wrong in a way worth recording,
because the correction is the whole decision.

**WebAuthn binds every credential to a Relying Party ID — a domain — and stores
it on the authenticator**, in the phone's secure enclave or the security key or
the password manager. The browser only offers a credential whose RP ID matches
the origin being visited. A server cannot rewrite what is on someone's phone, so
**changing the RP ID destroys every passkey registered under the old one**, and
the user finds out at sign-in, when the credential they now depend on silently
fails to appear. That is the worst failure shape available for an authentication
mechanism.

Two hard constraints narrow what an RP ID can be:

- **An IP address cannot be one.** It must be a domain. A Mosaic reached at
  `192.168.1.50:8443` cannot run WebAuthn at all, however it is implemented.
- **WebAuthn requires a secure context**, so HTTPS. A `.local` name cannot hold
  a publicly trusted certificate — the CA/Browser Forum stopped issuing for
  internal names in 2015 — so a `.local` install is self-signed.

**The thing the old framing missed is that a self-hosted install's origin
changes over its life**, and legitimately: raw IP at first boot, `<name>.local`
once the owner names the server during claim, and `mosaic.duckdns.org` or a
Tailscale name once they set up access from outside. That is three origins in
sequence. The blocker was never that a self-hosted server has no domain — many
have one, free, through DuckDNS, Cloudflare Tunnel or Tailscale. It is that
**nobody may enrol a passkey until the origin is final**, and in this sequence
the final one arrives last.

So it is not a decision the owner owes about *Mosaic's* domain. It is a policy
about when an install may offer enrolment, which is Mosaic's to make.

## Decision

**Username and password is the foundation and stays mandatory. Passkeys are an
optional second credential, offered only on an install that has a public origin,
and enabled by the superuser from that origin.**

- **Password authenticates every install, including LAN-only ones.** It works on
  an IP, on `.local`, on day one before anything is configured, and it is what
  the recovery path leans on. Nothing here weakens it and no install can be left
  without it.
- **The relying-party id is explicit configuration, not inferred.** It is set
  deliberately, never derived from the `Host` header of whatever request
  happened to arrive. Inference is the version of this that fails silently: a
  Mosaic reachable at both `mosaic.local` and `mosaic.example.com` would mint
  credentials under whichever name the enrolling browser used, and half of them
  would stop working depending on how the user came back.
- **It is Generation-class configuration** ([supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md), [supervisor#2](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0002-supervisor-guarantees-an-interface.md)'s front door).
  The public origin and the certificate are the same fact — HTTPS on
  `mosaic.example.com` requires a certificate for that name, and the RP ID must
  match the origin it is served on — so the two move together, and the front
  door is the Supervisor's.
- **Enrolment is offered only when a public origin is configured, and only when
  the user is on it.** On an IP-only or `.local`-only install the affordance is
  not drawn. This is the rule [platform#36](0036-telemetry-storage-retention-and-expert-mode.md)
  arrived at for the expert-mode toggle, for the same reason: routinely showing
  someone a control that fails teaches them the product is broken.
- **The superuser turns it on for the install; each user enrols their own.**
  Enabling it is an install-level decision about how this Mosaic is reached;
  registering a credential is personal, and one user adopting a passkey must not
  require another to.
- **Every credential records the relying-party id it was registered under.**
  `domain.PasskeyCredential` has no such field today, which is correct for a
  fixed RP ID and wrong for one that can change. With it, changing the origin
  becomes a message — *these four passkeys no longer work, re-enrol* — instead of
  a credential that quietly stops being offered.
- **Prefer the registrable apex where there is one.** A credential registered
  against `example.com` is usable on `mosaic.example.com` and every other
  subdomain, so moving the service between hosts inside one domain costs
  nothing. It is the only migration lever WebAuthn provides and it is worth
  taking by default.
- **Changing the origin is a destructive, confirmed action**, described in the
  words of its consequence rather than as a configuration edit.

## Alternatives

**Mandate passkeys, as the industry direction suggests.** *Rejected.* It
excludes every LAN-only install permanently — not by an implementation gap but
by WebAuthn's own rules — and a media server on a home network is a first-class
deployment rather than a degraded one.

**Drop passkeys entirely and add TOTP instead**, which needs no origin and works
everywhere. *Rejected as a replacement, still open as an addition.* The users who
expose Mosaic to the internet are precisely the ones facing credential stuffing,
and they are the ones who can have passkeys; refusing the strong option to the
population that most needs it, on the grounds that another population cannot use
it, is the wrong way round. TOTP remains the better answer for LAN-only installs
and is not decided here.

**Infer the RP ID from the request's origin.** *Rejected*, and it is the
tempting one because it needs no configuration and appears to work. It mints
credentials under whichever name the browser used, so a user who enrols from
inside the house and returns from outside finds nothing offered. Silent, and
diagnosable only by someone who already knows how WebAuthn works.

**Operate DNS for every install**, the Plex `*.plex.direct` pattern: public
records resolving to private addresses, so the browser reaches the LAN over an
origin with a valid certificate. *Rejected.* It gives a stable RP ID and a real
certificate for no user effort, and it costs Mosaic a DNS service every install
phones home to, plus certificate custody — done cheaply it means one wildcard
private key shipped to every user, which is the part Plex is criticised for, and
done properly it means running an issuance service. Neither belongs in a first
release.

**Allow enrolment on `.local` and accept the self-signed certificate.**
*Rejected on present evidence, and the evidence is thin* — see the open
question below. Even if browsers permit it, `.local` is the origin most likely
to be superseded by a public one later, so it is the worst place to let a
credential be created.

## Consequences

- **Passkeys will never be universal in Mosaic**, and the documentation should
  say so plainly rather than listing them as a feature and letting a LAN-only
  owner discover the exclusion.
- **A domain change becomes a destructive operation with a real consequence to
  explain.** The alternative was that it was destructive anyway and nothing said
  so.
- **`domain.PasskeyCredential` grows a field**, and the ceremony gains a
  precondition check that is as important as the cryptography.
- **This does not unblock the certificate.** A `.local` install still serves a
  self-signed certificate and still shows a browser warning on every new device;
  the domain is what fixes that, and it is now the owner's to have rather than
  Mosaic's to decide.
- **Open, and it decides how much the claim-time naming step is worth:
  whether a browser will run a WebAuthn ceremony on a `.local` origin behind a
  self-signed certificate.** WebAuthn needs a secure context and browsers
  restrict it further on origins with certificate errors; the exact behaviour has
  not been checked. If they refuse, naming the server does not move passkeys at
  all and only the tunnel or domain step does — which changes nothing in this
  record, since enrolment is gated on a public origin either way, but it does
  decide what an owner is told during claim. It is a browser check, not a design
  question, and it should be run before the ceremony is built.
