# The children listen on Unix sockets

**Status:** Built.
**Date:** 2026-08-08

## Context

[supervisor#2](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0002-supervisor-guarantees-an-interface.md) says the Supervisor is
the only public HTTP entry point and the Platform never serves UI directly. As
built, that is a **convention rather than a property**. The Platform listens on
two TCP ports — the client API and the Supervisor handoff — and the Shell on a
third. Nothing stops another process, another user on the box, or another host
on the LAN from talking to any of them; the front door is simply the address
people are told about. Bind one of them to `0.0.0.0` by editing a variable and
the Platform is on the network with no TLS, no rate limiting that means
anything, and none of the routing decisions the front door makes.

Two of those are especially wrong to leave reachable. The handoff listener
carries Generation, migration and configuration-activation state and
deliberately bypasses the policy gate because it is "internal" — a description
of intent, not of anything enforced. And the client API is where every
unauthenticated bootstrap arrives.

**Mosaic already decided this question, for somebody else's code.** An
extension module is reached over a Unix socket, and the reason is written down
at the call site: *"no port allocation, no accidental network exposure, and
filesystem permissions as the access control"*
([platform#39](0039-extension-module-boundary.md),
[sdk#7](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0007-go-plugin-as-the-extension-harness.md)). Third-party code gets
a boundary the OS enforces; Mosaic's own two processes are held apart by
convention. That asymmetry is backwards.

It is also load-bearing for something already found and unresolved. The one
pre-authentication surface is rate-limited per peer
([platform#57](0057-the-pre-session-bootstrap.md)), and the limiter keys on the
socket's peer address — which, now that every client arrives through the front
door, is the Supervisor's for all of them. The fix is for the Platform to read
the forwarded address, and the reason that fix has not been taken is that a
forwarded header is forgeable by anyone who can reach the Platform directly.

## Decision

**The Platform and the Shell listen on Unix domain sockets. The Supervisor
holds the only TCP listener, and is therefore the only way in.**

- **Both of the Platform's listeners move**: the client API and the handoff
  channel. They are separate sockets, because they are separate surfaces with
  different audiences, and collapsing them would publish the handoff to
  anything that could reach the API.
- **The Shell's listener moves** for the same reason, and because it has no
  business being reachable except through the door that serves it.
- **The sockets live in a runtime directory** owned by the user Mosaic runs as,
  with the sockets themselves mode `0600`. Filesystem permissions are the
  access control, exactly as they are for a module.
- **The listen address determines the transport.** An absolute path is a Unix
  socket; a `host:port` is TCP. This keeps one setting per listener rather than
  adding a mode flag beside it, and makes the shipped default — a path —
  visible as the thing it is.
- **A child unlinks a stale socket before binding.** A process killed with
  `SIGKILL` leaves the file behind, and refusing to start because the last boot
  was unclean would turn a crash into an outage.
- **`X-Forwarded-For` becomes trustworthy, and the Platform reads it.** This is
  the point that makes the change worth more than its tidiness: once the
  Platform is reachable only through a socket the Supervisor holds, the
  Supervisor is the only party that can set that header, so trusting it is not
  a judgement about proxies but a consequence of there being no other path in.
  [platform#57](0057-the-pre-session-bootstrap.md)'s per-peer ceiling starts working again as a result — and it would
  otherwise have got *worse*, because a Unix socket has no peer address at all
  and every client would share one bucket.

**What this does not claim.** A deliberate TCP bind is still possible, because
the address is configuration and configuration can be changed. The guarantee is
that the shipped shape has one door, that reaching a child requires local
filesystem access as the Mosaic user, and that exposing one is now a visible
act — a path replaced by a port — rather than the default nobody chose.

## Alternatives considered

**Leave them on loopback TCP and document it.** This is close to what the dev
overlay already achieves by putting the children on the Supervisor's own
loopback, and it is genuinely better than nothing. But loopback is shared with
every other process and every other user on the host, so it is a boundary
against the network and not against the machine — and it protects nothing on a
box where a media server is not the only thing running. It also leaves the
forwarded-header problem exactly where it is.

**A network namespace, or a container per process.** Stronger isolation, and it
buys the same property this does. Rejected as the *mechanism* because it moves
a guarantee Mosaic can make in its own code into the deployment, where it is
one `docker run` away from being absent; Mosaic ships a binary people run on a
box ([platform#38](0038-platform-binary-built-by-ci.md)), not only an image. A deployment that adds namespaces on top of
this is welcome to.

**Socket activation: the Supervisor creates the listeners and passes file
descriptors to the children.** This is how systemd does it, and it removes the
race where the Supervisor dials a socket the child has not yet bound. Rejected
for now as more machinery than the problem needs — the readiness probe already
handles "not up yet", and fd passing complicates the restart path the Supervisor
just gained. Worth revisiting if the race proves to matter.

**Keep TCP but require mutual TLS between the processes.** Solves
authentication, not exposure: the listener is still on the network, still
answering, still a surface. It also introduces certificate custody between two
processes on one box, which is a great deal of ceremony for a boundary the
kernel will enforce for free.

## Consequences

- **The default dev stack changes shape.** `docker-compose.dev.yml` runs the
  Platform and the Shell as separate compose services with published ports, and
  separate containers cannot share a Unix socket without sharing a mount. The
  front-door overlay already runs one process tree and is unaffected. The plain
  dev stack keeps TCP by setting ports explicitly — which is now a visible
  choice in a development file rather than the shipped default.
- **`curl localhost:8081` stops working**, and that is a real loss for
  debugging. `curl --unix-socket` replaces it, and the front door remains
  reachable normally.
- **The peer address disappears**, so anything that reasoned about it must read
  the forwarded header instead. There is exactly one such thing today, and it
  was already wrong.
- **Windows and macOS are fine.** `AF_UNIX` is supported on both and Go exposes
  it, so the desktop story does not need named pipes.
- **A socket is a file, so it needs a directory that exists and survives.**
  Nothing in Mosaic manages a runtime directory today; the Supervisor gains
  that responsibility, since it is the process that starts before the others.
