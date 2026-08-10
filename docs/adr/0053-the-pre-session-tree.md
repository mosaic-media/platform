# The pre-session tree, and what a locked door may say

**Status:** Accepted, built, and **withdrawn on 2026-07-25** — the code was
removed in contracts v0.48.0 and the Platform and Shell commits that used it were
reverted. The decision stands and the implementation did not: a pre-session tree
names components (`SignInPanel`) whose *definitions the client has never been
given*, because the definition library is pushed on connect — that is, after a
session exists. The Platform served exactly the right tree and the Shell drew
"SignInPanel — not registered in this Shell".

This record did not answer where a pre-session tree gets its vocabulary, and that
is the gap. Three ways out, none yet chosen: the screen endpoint returns the
definitions it needs alongside the tree; a pre-session screen is built from
primitives only; or the library becomes an unauthenticated fetch. Whichever is
taken belongs in a new record that supersedes this one.

**Superseded by [platform#57](0057-the-pre-session-bootstrap.md)**, which takes the
first of those three and carries the skin with it.

The failure is also a lesson about verification. The server half was checked end
to end and the browser half was declared blocked and skipped — and the browser
half was where the defect was. A screen that has not been rendered has not been
verified.

**Date:** 2026-07-25

## Context

Every screen Mosaic draws is server-emitted ([platform#21](0021-server-owned-app-shell.md), [contracts#8](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0008-one-generated-sdui-vocabulary.md)): the client
ships a primitive vocabulary and a generic expander, and the Platform sends the
tree. There is one screen this had never covered, and it is the first one anybody
sees.

The sign-in screen has no session, and **the session transport is where trees
come from.** `SessionService`'s every request begins with a session ref, which
is the point of it being a separate service from `AuthService` — the latter
exists precisely because sign-in is the one call made without one. So there was
no path by which a tree could reach a client that had not signed in yet.

What the Shell did instead is worth stating plainly, because it is the thing this
record replaces: it read a username and a password out of Vite environment
variables and signed in with them on load. There was no sign-in UI at all. A
build with `VITE_DEV_USERNAME` unset showed the Standby panel saying the server
could not be reached, which is not what had happened.

The roadmap carried this as "an ADR is needed — it is a pre-session tree, which
the contract has no path for". The second half of that sentence was true and the
inference drawn from it was not. `AuthService` is a service the Platform already
mounts without authentication; giving it a method that returns a `UINode` is an
addition, not a violation. **The absence of a path is not the presence of a
barrier**, and this is the second time in one week that reading it as one cost
real work — the app shell's chrome was recorded as unable to vary by route for
the same reason, and was five lines.

The decision that *is* real, and the reason this record exists, is a different
one: **what a locked door is allowed to say about the house behind it.**

The mockup draws three things on the unauthenticated screen that are facts about
the install: the household's profiles, as named circular avatars (Alex, Sam,
Kids); the library's size (`1,284 films · 216 series · 9,410 tracks`); and the
server's name (`home-server.local`). Each is useful — a household server that
makes you type a username when it knows there are three people is worse to use
than one that lets you pick a face — and each is disclosure to anyone who can
reach the port.

## Decision

**A pre-session tree is served by `AuthService`, not `SessionService`.** A new
unary `SignInScreen` returns a `mosaic.sdui.v1.UINode`. It is on the
unauthenticated service because that is what it is; putting it on the session
service would have meant a method there that does not take a session, which is
the distinction the two services exist to keep.

**It is a tree, not a hand-written screen.** The Shell's self-rendered UI stays
what [platform#21](0021-server-owned-app-shell.md) said it was: the states where the Platform is *unreachable*
(Standby, Reconnecting, Platform Offline). A reachable Platform that has not
authenticated you is not that — it can answer, so it should say what to draw. The
alternative was a hand-written sign-in panel in every client, which is the
per-client drift the whole SDUI thesis exists to prevent, on the one screen where
every client's version would be seen first.

**Exactly one action on that tree is client-interpreted.** A pre-session tree's
actions cannot be dispatched over the session transport, because there is no
session to dispatch them on. Rather than invent a second dispatch path, the
contract states that the sign-in tree carries a single `invoke` whose mutation is
`signIn`, and the client is required to interpret it by calling
`AuthService.SignIn` with the form scope's values. It is a narrow, named
exception rather than a general capability: no other pre-session action exists,
and a client encountering any other action on this tree should ignore it.

**The tree discloses nothing about the install.** No profiles, no library counts,
no server name. An unauthenticated caller is told that this is a Mosaic server
and asked for a username and a password, and that is all.

## Alternatives

**Hand-write the sign-in screen in each client**, as the no-session states are.
Rejected: those exist because the Platform cannot answer, and this one it can. It
would also have put the screen most likely to be seen by a stranger outside the
one mechanism that keeps clients from drifting.

**Serve the tree from `SessionService` with an empty session ref.** Rejected: it
makes "every request on this service carries a session" false, and that sentence
is load-bearing — it is why the two services can be mounted behind different
interceptors.

**Disclose the profiles and counts, as the mockup draws them.** Rejected *for
now*, and this is the closest call in the record. Jellyfin and Plex both show a
user list on their sign-in screens, and for a household server on a home network
that is a reasonable default: the threat it exposes you to is someone who is
already inside the network learning three first names. But Mosaic is also
reachable from outside a home network the moment anyone forwards a port, the
mockup's own Settings screen offers exactly that as a switch, and a decision to
leak by default should be taken deliberately rather than inherited from a
picture. It is a setting waiting to exist — "show who lives here on the sign-in
screen", off by default — and not a thing to ship implicitly.

**Keep the environment-variable sign-in and put the form behind it.** Rejected:
a development affordance that authenticates without asking should not survive
into a build anyone else runs, and it had already outlived the point at which
anyone would notice it was there.

## Consequences

The Shell renders a real sign-in screen, and a wrong password now says so instead
of reporting that the server is unreachable. `VITE_DEV_USERNAME` and
`VITE_DEV_PASSWORD` are gone.

**Four elements of the mockup are not built, and three of them are this record's
doing rather than a gap.** The profile row, the library counts and the server
name are withheld by the decision above. The fourth — "Forgot?" — is a genuine
absence: recovery factors exist in the credential store and no flow consumes
them.

**Two more are absent for want of a field.** "Keep me signed in on this device"
has nowhere to go: `SignInRequest` carries a username, a password and a device
id, and session lifetime is not a parameter of it. "Change server" is a client
concern — which Platform to talk to — and the web Shell reaches its Platform
through a dev proxy with no notion of a second one.

**The disclosure question now has a home.** When the profile row is wanted, it is
a preference on the install, read by `SignInScreen` and defaulting to off, and
this record is where the reasoning for that default lives.
