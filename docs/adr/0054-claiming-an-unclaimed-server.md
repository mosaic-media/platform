# Claiming an unclaimed server

**Status:** Accepted, built, **withdrawn on 2026-07-25** with the pre-session
surface it rode on ([platform#53](0053-the-pre-session-tree.md)), and **rebuilt on
2026-07-27** (M1.1) over
[platform#57](0057-the-pre-session-bootstrap.md)'s bootstrap and
[platform#61](0061-the-pre-session-action-lane.md)'s action lane.

Built in part, and the parts are worth naming. The claim, the setup screen and
the durable instance identity landed; the **claim audit record** and the **claim
window** are still unbuilt, exactly as the Consequences below name them. The
setup screen has **four steps rather than the one** this record could support
when it was written: the jobs runner has landed since, and a server-name field
and a stream-source connection turned out to be buildable, so three of the five
steps this record dropped came back.

**Date:** 2026-07-25

## Context

Mosaic's first administrator is created out-of-band. `bootstrap.EnsureAdmin`
runs at start-up from `MOSAIC_BOOTSTRAP_ADMIN_USERNAME` and
`MOSAIC_BOOTSTRAP_ADMIN_PASSWORD`, and the code that calls it says why:

> There is no in-band way to grant the very first authority — every command that
> could is itself policy-gated — so this bridges that gap for initial setup.

That is true and it is not a setup experience. `CreateLocalUser` authorises
`user.create`, which nobody holds on a server with no users, so the wizard the
designs draw cannot use it. Somebody installing Mosaic today either sets two
environment variables before first start or has no way in at all.

[platform#53](0053-the-pre-session-tree.md) built the half of this that was
missing: a tree can now be served before there is a session. What it did not
answer is who is allowed to become the owner of a server nobody owns yet.

## Decision

**An unclaimed server is claimed by whoever asks first.** `ClaimServer` is
unauthenticated, creates the first administrator with superuser permissions, and
**refuses once any user exists** — a second call is `Conflict`, not a second
owner. It returns a session, so the person who claimed the server is signed in
by the act of claiming it.

**The pre-session screen endpoint answers with the state the door is in.** There
is one endpoint ([platform#53](0053-the-pre-session-tree.md)'s `SignInScreen`) and it serves the setup tree while
the server is unclaimed and the sign-in tree once it is not. A doorway has two
states and this is which one you are looking at; a second endpoint would have
meant a client asking "which screen should I show" before it could show one.

**The environment-variable bootstrap stays.** It is how an automated deployment
provisions a server it never puts a human in front of, and removing it would
break every existing install. A server bootstrapped that way has a user, so it
is already claimed, and the setup tree is never served for it.

## Alternatives

**A console claim token** — the Platform prints a one-time secret to its own
stdout at first boot and the claim requires it. This is the stronger design and
it was the recommendation. It makes the property "whoever can read the server's
console owns the server", which is the one an operator actually wants, and it
fails safe on a port that is reachable before setup finishes. It was not taken:
it costs a paste from a log during setup, and for a home server installed on a
machine the installer is sitting at, that cost is paid on every install to
defend against a window that is usually seconds long. **Recorded because the
trade was made deliberately and the threat below is its price.**

**Plex's model** — claim against an account held on a central service. Rejected
outright: Mosaic has no central service and is not going to acquire one to
answer this.

**Leave the environment variables as the only path.** Rejected: it is the reason
there is no setup experience, and it puts a password in a shell history or a
compose file on every install.

## Consequences

**The threat is real and it is accepted.** On a server reachable from a network
before somebody sits down to set it up, the first party to find it becomes its
owner. Jellyfin ships exactly this. The window is from first start to first
claim, and the mitigations that exist are operational rather than architectural:
do not expose the port before setup, and if a server is found already claimed,
it was.

Two things follow that should be built and are not:

- **The claim should be observable.** A claim is the single most consequential
  unauthenticated write the Platform accepts and it currently records a
  `user.created` event like any other. It wants an audit record that says the
  server was claimed, from which address, so an operator who finds a server
  claimed can see when and by whom.
- **A claim window would cost little.** Refusing claims more than N minutes after
  start-up, with a documented restart to reopen it, would shrink the exposure
  from "until somebody notices" to "until the timer expires" without the token's
  paste. It is the obvious next increment if this proves uncomfortable.

**The setup screen is one step, not the design's six.** Owner creation is what
can be built; the other five steps need capability that does not exist — a
server-name field, a filesystem scanner, service connections, a jobs runner, and
playback settings anything reads. The rail shows the steps that exist rather
than six of which four do nothing, and the design's own footnote already says
the rest can be changed later in Settings.
