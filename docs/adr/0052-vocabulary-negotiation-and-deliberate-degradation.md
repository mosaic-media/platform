# Vocabulary negotiation and deliberate degradation

**Status:** Accepted (built)
**Date:** 2026-07-25

## Context

[contracts#8](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0008-one-generated-sdui-vocabulary.md) made the SDUI vocabulary one
generated thing and published it as data. That answered *what the contract
contains*. It did not answer the question the server actually has to ask before
it sends anything: **can this client draw what I am about to send?**

Nothing on the wire carried the answer. The client declared what it could
*decode* — [web#4](https://github.com/mosaic-media/web/blob/main/docs/adr/0004-player-as-client-primitive.md)'s `ClientProfile`, added
because the Platform had been hard-coding a desktop browser's codecs at the
selection call site — and declared nothing at all about what it could *render*.
So the server emitted the whole vocabulary at every client and the client turned
what it did not know into a labelled placeholder.

That placeholder is a real guarantee and it is not the problem. The problem is
that it was **silent**. A screen with a hole in it looked identical to a screen
without one to everybody except the person on that page, which is the same
failure shape as `ui.Subtitle` on a `Stack` — a thing that draws nothing and
reports nothing. A client running a release behind the contract could stay that
way indefinitely, and the first symptom would be a user describing a missing
control.

The numbers make it concrete. As of [contracts#8](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0008-one-generated-sdui-vocabulary.md) the contract declares 26 primitives
and 12 action kinds; the one client implements 25 and 9. That gap is deliberate —
`Form`, `query`, `setValue` and `submit` belong to slices that have not landed —
but until now it was a fact you could only establish by reading two repositories
side by side.

## Decision

**A client declares the vocabulary it implements, and the server emits only
that.**

- **`VocabularyProfile` rides Attach**, beside `ClientProfile` and for the same
  reasons: it cannot change without a reconnect, Attach is the one call every
  client makes on every connect, and the Platform's live-session state is
  disposable so a re-declaration is what puts it back. It carries the vocabulary
  *version* the client was built against and the precise sets of primitives and
  action kinds it implements.
- **The version is not the claim; the sets are.** A client may be built against
  vocabulary 1.0.0 and implement 25 of its 26 primitives. The version is recorded
  so version skew is visible in telemetry; the filtering reads the sets.
- **An absent declaration means "send everything."** Exactly the behaviour every
  client had before the field existed, and the same shape as `ClientProfile`'s
  undeclared case — "this client renders nothing" and "this client said nothing"
  are different answers and are treated differently.
- **Only the primitive tier is filtered.** Components are definitions the server
  delivers, so a client renders whatever it is sent; a client that declared
  components would be claiming to implement things it is supposed to receive.
  Module types pass through untouched for the same reason.
- **A node of an undeclared primitive is dropped whole, with its subtree.**
  Keeping orphaned grandchildren in the parent's place rearranges a layout rather
  than simplifying it.
- **An action of an undeclared kind is stripped from its node, and the node
  stays.** Removing the control removes an affordance the server decided the
  screen needed; leaving it inert is the smaller change, and the client may have
  its own rendering for a control with nothing wired to it. A `sequence` is
  all-or-nothing: half a sequence is a change nobody asked for.
- **Every degradation is reported.** One telemetry event per push, with the types
  and kinds and their counts. This is the entire point of the slice — the client
  would have drawn a placeholder either way; what changes is that somebody can
  know.
- **A definition may declare a `fallback`** ([contracts#8](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0008-one-generated-sdui-vocabulary.md)'s
  contract, used here): a second template for a client missing a primitive the
  first one needs. The server picks per session and sends one, so the client never
  sees both and never has to choose. Node-level degradation cannot reach inside a
  template — the server emits a component by name and the client expands it — so
  this is the only place a component can say what it becomes on a client that
  cannot draw it in full.
- **A definition that needs a missing primitive and has no fallback is served
  unchanged**, and reported. Omitting it would turn every node that uses it into
  an Unknown placeholder, which is strictly worse than a template with a hole.

**The declaration is derived, never maintained.** On the web client the
primitives are the key set of the registration map itself, the action kinds are
tied to the dispatcher's `Action` union by a compile-time assertion in both
directions, and the union is tied to the dispatcher's `switch` by an
exhaustiveness check. A build-time script then measures the declaration against
the contract's conformance fixture and fails if the client claims a type the
contract does not have, or if the stated gap has changed in either direction. A
*wrong* declaration is worse than none — it has the server carefully degrade a
screen for abilities the client actually had — so none of it is trusted on its
word.

## Alternatives considered

**Send everything and let the client cope.** *Rejected* — this is the status quo
and it is what the record is about. It is not that placeholders are bad; it is
that nothing anywhere knew one had been drawn.

**Negotiate on version alone.** *Rejected* — it makes the version a claim to
implement everything in it, which is false the moment a client is partway through
implementing a slice. The one real client is in exactly that position today.

**Have the client filter what it renders.** *Rejected* — it inverts the
direction of authority and, more practically, the client is where the information
about *what was left out* dies. The point of moving the decision to the server is
that the server has telemetry.

**A client-to-server telemetry lane, so the client's own unknown-type sightings
are reported centrally.** *Deferred, not rejected.* It is a decision about what a
client may report unprompted, and it needs the per-node identity that would make
such a report attributable. Both belong with lifecycle triggers and analytics
identity. Until then the client reports an unknown type to its console with the
current trace id — never silently, but not upstream either.

**Let a definition declare a per-primitive substitution instead of a whole
fallback template.** *Rejected* — it is an expression language in disguise, and
the substitution rules would have to live in every client. A second template is
data, and the server chooses between them.

## Consequences

- **A client behind the contract is now a log line rather than a discovery.**
  `missing_primitives=Form missing_actions=query,setValue,submit` on every attach.
- **A fallback template is a second copy of a template**, with the maintenance
  cost that implies. Lint requires it to use strictly fewer primitives than the
  one it replaces, so at least it cannot silently stop being a degradation. No
  definition in the shipped library declares one yet, because nothing in it needs
  one.
- **The degradation pass runs on every push**, and it allocates a new tree for
  any client that declared a vocabulary. For an undeclared client it returns the
  same pointer and does nothing.
- **The message constructors now take the session.** That is the enforcement: a
  new push path cannot skip degradation, because it cannot build the message
  without handing over the declaration that decides what to send. A pass
  remembered at each call site is one a later call site forgets.
- **The action-stripping heuristic has a stated boundary.** A props value is
  treated as an action only when its `kind` is one the contract declares, so a
  screen param that happens to carry a field called `kind` is left alone. A false
  positive here would silently delete real data, which is why the check is against
  the contract's set rather than the presence of the key.
