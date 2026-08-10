# One client transport: retire GraphQL

**Status:** Accepted — built (the GraphQL transport is deleted; `AuthService`
mints sessions over Connect). Partly superseded: the removal of the `query`
action kind was reversed by [contracts#8](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0008-one-generated-sdui-vocabulary.md),
which declares a `query` carrying a screen name rather than a GraphQL string;
the rest stands.
**Date:** 2026-07-22

## Context

[contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md) moved the first-party
client onto a two-lane Connect surface and, under "The GraphQL question", named
three ways the two transports could coexist. It chose **split by audience**:
Connect for first-party clients, GraphQL kept "only where a flexible, ad-hoc
query surface genuinely earns it — external integrations, tooling, exploration."
It explicitly deferred the third option, *consolidate*, as "the largest
migration."

Six months of building against that split is enough to judge it, and the split
did not hold. What was actually true on disk when this was written:

- **The retained surface had no consumer.** The GraphQL schema exposed users,
  permissions, config versions, the nine content commands, module catalogs, and
  jobs/health stubs. Nothing called any of it. The Shell used exactly one
  operation, `signIn`; every other first-party interaction had already moved to
  `SessionService`. The rest was exercised only by its own tests.
- **The hypothesised audience did not arrive.** "External integrations and
  tooling" was a prediction, not a requirement — no ADR asks for a public query
  API, and Mosaic is a self-hosted product whose extension story is *modules
  compiled in* ([platform#4](0004-static-go-module-composition.md),
  [platform#15](0015-module-capability-and-invocation.md)), not third parties
  querying over HTTP. A module reaches the Platform through the SDK, in-process.
  There was no one on the other end of the flexible surface.
- **Two transports meant two implementations of the same command.** `importContent`,
  `configureModule` and `playPart` existed as a GraphQL resolver *and* as a case
  in the session transport's `dispatch`, each decoding the same action envelope.
  That is the cost the split was supposed to avoid, paid anyway, and it is the
  shape a divergence bug grows in.
- **The one surface a client did use was the one GraphQL served worst.** GraphQL
  returns HTTP 200 whatever happened, so the Platform's error categories had to
  travel in an `extensions` bag — which the handler never actually populated.
  The Shell's `normaliseCategory` read a field that was never sent and defaulted
  everything to `Internal`. A failed sign-in and an unreachable Platform were
  indistinguishable to the client for as long as this existed.

So the split was not a boundary between two audiences. It was one live operation
and a large unused surface, held in place by an ADR rather than by a caller.

## Decision

**Connect is the only client transport. The GraphQL transport is deleted.**

The client API is two Connect services and nothing else:

- **`mosaic.auth.v1.AuthService`** — `SignIn`, `SignOut`. New in this record.
  It is the one call made *without* a session, which is why it is a service of
  its own rather than more RPCs on `SessionService`: every request on that
  service begins with a session ref, because a session intent is by definition
  something a session does. Separating them also lets the two mount behind
  different interceptors.
- **`mosaic.session.v1.SessionService`** — the two lanes of
  [contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md), unchanged.

Three consequences of "delete" are decided here rather than left to discover:

**The unclientled surface goes, and comes back as screens.** Users, permissions,
config versions, jobs and health had no caller and no UI. Reimplementing them as
Connect RPCs would have preserved a surface nobody reached, in a second shape,
before knowing what an admin UI needs. When Mosaic grows one, it arrives the way
every other surface does — as a server-emitted screen
([platform#19](0019-sdui-emit-side.md)) whose affordances dispatch actions through
`Invoke`. That is the Platform's own answer to "how does a user do a thing", and
it is not an exception worth making twice.

**The content commands lose their transport, and `dispatch` becomes the
boundary.** `AddContentWork` and its eight siblings remain on the published SDK
surface ([platform#12](0012-published-contract-surface.md)) and are reachable
in-process by any module. What they no longer have is a way for a *client* to
call them by name. The session transport's `dispatch` switch is now the complete
enumeration of what a client can invoke — an action it cannot map does not
exist. That is a stricter surface than a schema, deliberately: adding to it is a
readable, reviewable act in one file.

**Error categories become status codes.** The Platform's seven categories map
onto Connect codes once, in a shared `transport/rpc` package that also owns the
telemetry seam ([platform#33](0033-instrument-at-the-seams.md)). This is the thing
GraphQL could not do, and it is why a wrong password is now
`UNAUTHENTICATED` at the client rather than a 200 the client has to interpret.

### What is deliberately not carried over

`mosaic.auth.v1.Session` has **no `capabilities` field**, though
`domain.Session` models one and the Postgres store round-trips it. Nothing
populates it at issue time, so the GraphQL surface returned an empty list on
every sign-in. A contract that always sends an empty list is worse than one that
omits the field: a client would reasonably read "no capabilities" as "hide
everything". Capability-gated affordances
([platform#24](0024-capability-gated-affordances.md)) therefore remain a server
concern — the Platform omits what the caller cannot use when it renders the
screen. Adding the field later is backward-compatible.

The SDUI `query` action kind is **removed from the contract**. It carried a raw
GraphQL query string and a region to refresh into; with no GraphQL endpoint it
is unimplementable, and nothing ever emitted one. The `invoke` kind keeps its
`mutation` field name — it names a Platform write, whatever the transport
carrying it is called, and renaming a wire field across the schema, both
bindings, the emit-side and the storybook would be churn for a synonym.

## Alternatives considered

**Keep the split (do nothing).** *Rejected* — this is the option under review,
and the evidence above is what rejected it. A second transport that no client
uses is not optionality, it is a second place for the command boundary to be
implemented slightly differently.

**Port the whole schema to Connect (`AdminService`, `ContentService`).**
*Rejected.* It preserves parity with a surface that had no caller, at roughly
2,000 lines of new transport code plus protos, and it front-runs the design of
an admin UI that does not exist. Parity with zero is zero.

**Put `SignIn` on `SessionService`.** *Rejected*, narrowly. It avoids a proto
file and a generated package. But every other request on that service starts
with a session ref and is authenticated on it; sign-in is the sole exception on
both counts, and hiding the exception inside the service makes the service's
contract harder to state, not easier.

**Keep GraphQL for sign-in only.** *Rejected.* It is the worst version of the
split: an entire second transport, client library, dev proxy entry and error
model retained for one call — and specifically for the call whose failure
reporting GraphQL handles worst.

## Consequences

- **One contract framing for clients.** [contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)
  worried about "a second contract framing in the codebase"; that worry is now
  resolved in the direction it pointed. A client — React today, Flutter/Compose/
  Swift later — generates from the protos and speaks nothing else.
- **This supersedes [contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)'s "split by audience".** The rest of [contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md) —
  two lanes, protobuf `UINode` ([contracts#6](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0006-contracts-protobuf-workspace.md)),
  resume cursors — is untouched and still Accepted.
- **There is no ad-hoc query surface.** If an integration ever genuinely needs
  one, it is a new decision with a real requirement behind it, not a
  reinstatement. That is the honest trade: this removes an option that was being
  paid for and not used, and reacquiring it costs a new transport.
- **The end-to-end proof got better.** The "make it runnable" test drove real
  PostgreSQL through the GraphQL HTTP handler — a path no client took. Its
  replacement signs in over `AuthService`, then subscribes and navigates over
  `SessionService`, asserting the pushed content region. It proves the same
  stack through the only transport there now is.
- **Some Platform capability is genuinely unreachable from a client**: creating
  roles, granting them, drafting/activating config versions, and setting user
  status have application services, policy actions and tests, but nothing a user
  can press. That was already true — a GraphQL mutation with no UI is not a
  feature — but it was easy to mistake the schema for a surface.
  `bootstrap.EnsureAdmin` remains the only in-band way to establish the first
  authority.
- **The gap gets a register rather than a sentence.** Deleting a surface makes
  its absence invisible: the application services keep their tests, so the build
  stays green and the roadmap keeps saying "done". [Unreachable
  capability](../unreachable-capability.md) enumerates every affected operation,
  classifies it (owed / migrated / never worked), and states what discharging it
  requires. Enumerating the deleted schema to write it surfaced a case this ADR
  did not cause and would otherwise have missed: `app.CreateLocalUser` is a
  complete, well-tested command that **no transport has ever exposed**, so Mosaic
  has never had a way to create a second user. Adding a row to that register is
  now part of removing a client path.

## Implementation implications

Landed in this change:

- **mosaic-sdui** — `proto/mosaic/auth/v1/auth.proto` with `AuthService`; Go and
  TS bindings generated; `./auth` added to the npm export map. The `query`
  action kind removed from `schema/sdui.schema.json`, the generated bindings and
  the Go/TS authoring layers.
- **platform** — `internal/transport/auth` (Connect handler over
  `AuthenticateLocalUser`/`RevokeSession`, with its own boundary test);
  `internal/transport/rpc` holding the category→code mapping and the telemetry
  interceptor, the latter moved out of `session` and parameterised by component
  name so `auth` and `session` label their own records.
  `internal/transport/graphql` deleted, `/graphql` unmounted, `graphql-go`
  dropped from `go.mod`, and the end-to-end test rewritten over the two Connect
  services.
- **web** — the `gql` client deleted from `@mosaic-media/sdui-react` along with
  its `invoke`/`query` fallback; `devSignIn` reimplemented over `AuthService`;
  the shell's dead `fetchScreen` removed; one shared Connect transport so both
  services carry the same traceparent interceptor
  ([platform#32](0032-the-correlation-id-is-the-trace-id.md)); the dev proxy and
  compose healthcheck repointed off `/graphql`.

Not done here, and named so it is not mistaken for done: **`SignOut` has no
caller.** The Shell signs in on boot and never signs out, because it has no
sign-out affordance and no login form. The RPC exists and is tested; wiring it
is part of whatever lands real authentication UI.
