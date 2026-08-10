# Authorization is carried in the type

**Status:** Accepted (built)
**Date:** 2026-07-23

## Context

Every application service handler follows the same opening sequence: validate the
command shape, authenticate the caller, authorize the action through policy, then
act. It is written out by hand in each of the forty handlers on `app.Service`,
and it has been correct in all forty. Nothing enforced that. Go has no
annotations and no runtime proxies, so a handler that omits the preamble compiles,
passes its own tests, and serves reads to anyone who supplies a made-up session
id. The rule was documentation plus discipline.

Phase 4 span data made the cost of that arrangement visible, from the other
direction. One search — ten results from one Stremio addon — produced ten
`SELECT sessions`, ten `authorize content.read`, ten `SELECT roles` and nine
`SELECT nodes`: thirty-nine queries and ten full authenticate-plus-authorize
cycles for one user action, about 60ms of a 512ms request.

The cause was not a forgotten check. It was the opposite. `SearchAvailableContent`
marked each result in-library by calling `resolveInLibrary`, and `resolveInLibrary`
had only one way to read the content graph: `FindContentByExternalID`, a public
application-service entry point. Calling it did exactly what it is supposed to do —
authenticate, authorize, then read — once per result, for the same caller, the same
action and the same resource the handler above had already cleared. The developer
followed the rule. The rule fired ten times.

Both problems are the same missing distinction. **`Service` had one kind of
entry point.** Every method assumes it is being called from outside, because
there was no way for a function to say "I am already inside the boundary".

It is worth being precise about why the obvious import does not help here. In a
proxy-based container the fix reads as an annotation on the method, and
self-invocation — a method on a bean calling another method on the same bean —
bypasses the proxy entirely and runs with no advice at all. Applied to this code
that produces the inverse bug: not ten authorization checks, but zero, silently,
with no test able to see it. The mechanism that looks like it would have prevented
this would have converted a latency defect into an authorization hole.

## Decision

**Split the boundary into an outer and an inner form, and carry the proof in the
type.**

- **`enter` is the outer form.** `Service.enter(ctx, caller, action, resource)`
  runs gates 2 and 3 once and returns an `authorized`. Handlers call it in place
  of the hand-written preamble, so the ordering of authenticate-then-authorize is
  a property of one function rather than of each handler remembering it.
- **`authorized` is the proof.** An unexported struct holding the resolved
  `domain.UserID` and the caller, with `enter` as its only constructor. A function
  that takes one is inside the boundary; a function that takes a `v1.Caller` is an
  entry point that is not.
- **Inner helpers take `authorized` and read stores directly.** `resolveInLibrary`
  is the first: it now requires an `authorized`, does not read it, and goes
  straight to `NodeStore.FindByExternalID`. The parameter is a proof obligation,
  not data.
- **The caller is retained inside `authorized`, deliberately.** Forwarding it into
  a module must stay possible: a module's own writes re-authorize as the invoking
  user ([platform#13](0013-how-a-capability-acts.md)), which is why a module is
  handed a `Caller` rather than a Service with the boundary pre-cleared. That is a
  deliberate act at a module seam, not something an internal helper does by
  accident.
- **A conformance suite replaces the annotation's guarantee.** Two halves, and it
  needs both. A table of every caller-bearing method asserts `Unauthenticated` for
  a session that was never issued and `PermissionDenied` for a real caller holding
  no grants. A reflection pass over `*app.Service` then asserts the table is
  exhaustive: any exported method taking a `v1.Caller` or a `domain.SessionID` must
  appear in the table or in an exemption list that states a reason. **A new handler
  cannot be added without a row.**

The command handler order in `platform/CLAUDE.md` is amended accordingly: steps 2
and 3 are `enter`, and a handler's internal helpers take the `authorized` it
returns.

## Alternatives considered

**Leave it as convention and fix the loop.** *Rejected.* It fixes the instance and
not the class. The type change found a second, unreported instance of the identical
defect in `ListCatalogItems` — the catalog browse that renders the home screen —
which convention had not found in the time it had been there.

**Cache the session and policy decision for the duration of a request.** *Rejected.*
It would have made the redundant work cheap rather than absent: still ten policy
evaluations, still a per-request cache to invalidate, and the structural problem —
no inner form — untouched and now harder to see, because the symptom that exposed
it would be gone.

**A middleware or interceptor at the transport.** *Rejected as insufficient, not as
wrong.* The Connect interceptor in `internal/transport/rpc` already sits there and
is the right place for what it does. It cannot help here: it sees `Invoke`, and
which policy action that `Invoke` requires is inside the JSON action envelope. It
also cannot see calls that never cross a transport, which is every one of the ten
in the loop.

**Generate the wrapper from a declared action-per-handler table.** *Deferred.* This
is Go's real analogue of weaving, and it would additionally catch a handler naming
the *wrong* action, which the conformance suite does not. It is not yet worth the
generator: the client-reachable action list is three entries long
([platform#37](0037-one-client-transport.md)), and the declaration table would itself
become a thing to keep in sync. Revisit when the dispatch switch outgrows being
readable in one screen.

**Make the stores unreachable without an `authorized` by moving them behind a
subpackage.** *Deferred.* This is the strongest version — it would make the
compiler reject an unauthorized store read anywhere, not merely make an authorized
one expressible. Within `package app` the token is a convention among consenting
functions, since anything in the package can construct the struct. The cost is a
restructuring of `Service` around a boundary that has not yet been shown to leak.

## Consequences

- **Thirty of the search's thirty-nine queries are gone**, and with them the ten
  authenticate-plus-authorize cycles — about 45ms of the ~60ms the span data
  attributed to this. The remaining per-result `SELECT nodes` is a batching
  problem, not an authorization one, and needs a `FindByExternalIDs` addition to
  `NodeStore`; it is not done here.
- **A second instance was found by the compiler**, in `ListCatalogItems`. That is
  the argument for the type change over the point fix, stated as an outcome
  rather than a prediction.
- **The suite states what it does not check.** It catches omission of a gate, not
  the naming of a wrong action. Omission is the failure that actually happens;
  claiming more than that would make the suite worse than useless.
- **`FirstPlayablePart` was the worst instance of the pattern**, and is fixed.
  It is an entry point — the screens transport calls it directly to decide
  whether to offer Play ([platform#24](0024-capability-gated-affordances.md)) — so
  it keeps its `v1.Caller` and clears the boundary itself, once, then reads
  stores directly. It previously re-entered `GetContentNode` and then
  `ListNodeParts` per child, so a work whose playable item was its fifth child
  cost six session reads and six policy evaluations to learn one Part id; the
  node read was spent on a value it discarded, since only the children were
  used. Eighteen store and policy operations became eight.
- **Its signature grew an error, and that is a behaviour change.** It returned
  `(Part, bool)` and swallowed everything into `false`, so a caller denied
  `content.read` — or one whose session had expired — saw a detail screen with
  no Play button, indistinguishable from a work that genuinely has no bytes. A
  boundary failure is now returned. A *store* read that fails still degrades to
  "nothing playable", because a transient blip should omit one button rather
  than fail a screen whose metadata already arrived. The exemption list is
  consequently empty: every caller-bearing method is covered by behaviour.
- **Two telemetry defects were fixed alongside**, both from the same shadowing
  mistake: `search_available_content.go` and `import_content.go` reassigned `ctx`
  from `moduleSpan` and kept using it after `span.End()`. Everything the Platform
  did afterwards was recorded beneath a closed parent *and* attributed to the
  module — including the dedup reads above. "Was it us or the addon?" is the
  question those spans exist to answer
  ([sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)), and for those spans the
  recorded answer was wrong.
- **Migration is incremental.** Handlers move to `enter` as they are touched; the
  conformance suite covers all forty regardless of which form each one currently
  uses, because it asserts behaviour at the entry point rather than the presence
  of a call.

## Implementation implications

`authorized` and `enter` live in `internal/platform/app/service.go`, beside
`authenticate` and `authorize`, which they compose and do not replace — the
session-id handler family still calls them directly until it migrates.

The conformance suite is `internal/platform/app/boundary_conformance_test.go`,
in `package app_test`, so it reaches `*app.Service` only through its exported
surface — the same surface a transport has.
