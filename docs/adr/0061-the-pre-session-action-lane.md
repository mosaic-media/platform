# The pre-session action lane

**Status:** Accepted and built (M1.1, M1.2). Extends
[platform#57](0057-the-pre-session-bootstrap.md), which decided how a doorway
*arrives* and left open how its controls act. Carries
[platform#54](0054-claiming-an-unclaimed-server.md)'s claim onto that lane.
**Date:** 2026-07-27

## Context

[platform#57](0057-the-pre-session-bootstrap.md) gave the doorway a vocabulary: one
unauthenticated RPC answering with the skin, the definitions the tree needs and
the tree, so a client with no session has something to draw. It deliberately
delivered no controls — "a control wired to nothing is the dead end
[platform#24](0024-capability-gated-affordances.md) names" — and left sign-in and
claiming to M1.

M1 is where the controls arrive, and they arrive into a gap. Every other
affordance in Mosaic emits an SDUI action that the session transport's
`dispatch` maps to an application command
([contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md),
[platform#37](0037-one-client-transport.md)). A doorway has no session, so it has
none of that: no `Invoke`, and — more awkwardly — no push lane for an outcome to
travel back on. The two-lane transport exists precisely so the server drives the
client's regions unprompted, and a client that has not authenticated has nothing
for the server to drive.

There is also a shape to protect. The client bundles no components
([contracts#7](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0007-components-are-authored-only-in-the-contract.md)) and decides
no screens ([platform#21](0021-server-owned-app-shell.md)); the whole reason the
doorway is a server-emitted tree is that it can be redesigned without a client
release. A door whose *buttons* the client understood would give that back
immediately — the four-step setup wizard could not have been added without
shipping a new Shell.

## Decision

**`AuthService.Invoke` is the pre-session counterpart of the session
transport's `Invoke`, and its outcome rides the unary response.**

- **One RPC, one dispatch switch.** The switch enumerates what an
  unauthenticated caller may ask for — today `signIn` and `claimServer` — in one
  readable place, exactly as the session transport's does. An action a client
  can name and the server cannot map does not exist.
- **Three outcomes, as a `oneof`.** A minted session; a replacement doorway;
  or the fields that were refused
  ([contracts#13](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0013-validation-and-the-symmetric-rejection.md)'s envelope,
  unchanged). Exclusive rather than a set of fields, so a client cannot get the
  ranking wrong. Transport failures stay Connect errors.
- **The client interprets none of them.** It sends the name the server wrote
  into the tree and applies whichever outcome comes back.
- **The request carries the same vocabulary declaration `Bootstrap` carries**,
  so a replacement doorway is negotiated exactly as the first one was
  ([platform#52](0052-vocabulary-negotiation-and-deliberate-degradation.md)), and
  it carries its own definition subset for the same reason `Bootstrap` does.
- **It shares the bootstrap's rate limit.** They are one surface from an
  abuser's point of view, and two independent limits would each be reachable in
  full.
- **`SignIn` stays a method of its own.** An automated client holding
  credentials should not have to render a door to use them.

## Alternatives considered

**A client-side dispatch table** — the Shell maps `signIn` to `AuthService.SignIn`
and `claimServer` to a claim RPC. *Rejected.* It is the cheapest thing to build
and it puts the meaning of the door in the client, which is the one property
this whole thread has been spending releases to remove. The setup wizard is the
proof: four steps, a picker populated from the module repository, and a
review — none of it required a line in the Shell.

**Carry the outcome on a push lane opened before authentication.** *Rejected.*
A stream per unauthenticated caller is a resource an unauthenticated caller can
allocate, and the outcome of a doorway action is a single answer to a single
question. The unary response is the right shape and the cheaper one.

**Return the next tree for every step of the wizard**, making the server hold
the flow. *Rejected*, and this is the one worth stating: each step's answers
would have to travel back down inside the next tree, including the password the
second step collects. The wizard is one tree with one State scope instead, with
steps hidden by `visibleWhen` — which is why that prop grew onto `Box` in the
same change.

**Fold the claim into `Bootstrap`** as a second request field. *Rejected.* It
would make the one call every client begins with also a write, and a read that
is sometimes a write is a read nobody can reason about.

## Consequences

- **The unauthenticated write surface is now two calls, not one.** Both are
  rate-limited together and both are enumerated in one switch. That surface is
  the thing to keep looking at: [platform#54](0054-claiming-an-unclaimed-server.md) accepted the threat that whoever
  arrives first owns an unclaimed server, and this is the lane they arrive on.
- **`contracts.RejectFields` has its first callers.** It had been implemented,
  routed and never called since validation landed; the claim and the
  create-account form both produce one.
- **A multi-step form is validated by the server, not the client.** A hidden
  `Box` unmounts its inputs, so their rules leave the scope — the client's own
  check covers only what is on screen. A producer building one has to write its
  rejections to stand alone in the form-level message, because that is the part
  the person can see. This is a real cost of choosing one tree over a round trip
  per step, and it is the right trade only because the alternative moves a
  password.
- **A form-level rejection lands where the *server* put it.** A pushed
  form error is written into the scope variable named `formError`, and a
  producer that wants it shown binds a node's text to it. The alternative —
  the client drawing the sentence itself — would have put the one message a
  refused sign-in shows in the one file no designer can reach.
