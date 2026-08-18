# A manifest names one capability, and what it asks for is separate from what it offers

**Status:** Accepted. **Not built.** Closes the last open question in
[architecture](https://github.com/mosaic-media/architecture/blob/main/docs/index.md)'s
deliberately-undecided list. **Breaking:** changes what `ParseManifest` accepts,
so a Platform, `modulesign` and every published manifest move together.

## Context

The manifest was left minimal on purpose
([platform#15](0015-module-capability-and-invocation.md),
[platform#16](0016-optional-module-composition.md)), with what it grows to carry
recorded as open. M7 answered most of it by accumulation rather than by asking:
six records each added a declaration to it — the authority a module needs
([platform#85](0085-a-modules-authority-is-declared-and-consented.md)), a verb
and its input ([platform#86](0086-a-module-verb-is-declared-and-dispatched-by-name.md)),
a subscription and a schedule
([platform#87](0087-module-lifecycle-events-progress-and-schedules.md)), a
storage quota ([platform#92](0092-module-storage-is-granted-not-enforced.md)), a
path prefix ([platform#94](0094-a-gateway-is-invoked-from-outside-and-holds-no-authority.md)),
and which settings fields are secret
([platform#96](0096-module-settings-are-merged-and-secret-fields-are-sealed.md)).

Three things about the manifest were never settled, and one of them is now a
live hazard rather than a tidiness question.

**Whether the declared unit is the module or the capability.** `Manifest.ID` is
documented as the key the Platform registers *the capability* under, and
platform#85 attached grants to *the module*. Those have never disagreed, because
`host.Serve` takes exactly one capability: one process serves one capability, and
the distribution manifest projects onto the SDK manifest through `toV1Manifest`
with no room for a second.

**What a Platform does with a manifest it does not fully understand.** There are
two manifests and the answer differs between them. The SDK one is `Manifest()`
rendered to JSON by `--mosaic-manifest`. The distribution one is that document
plus the schema, the SDK major and the per-platform digests, which
`modulesign build-manifest` combines and a release publishes. `ParseManifest` is
the only reader of the second, called by the Platform's installer and by
`modulesign` in three places — and it calls `DisallowUnknownFields`, then refuses
any `schema` string but `mosaic.module.manifest/v1`.

So today the manifest cannot grow at all. Adding one optional descriptive field
makes every already-installed Platform refuse every module that carries it, and
bumping the schema string to say so refuses them anyway. That is the strictest
possible answer and it was the right default while the format had nothing in it;
it stops being right the moment six records are queued to add fields.

The tempting correction — drop `DisallowUnknownFields` — is worse than the
problem. Every one of those six declarations would then be silently skipped by a
Platform that predates it. A module declaring a storage quota would install and
be denied its first write; one declaring a path prefix would install and never be
reachable. Worse, platform#85 makes **consent the thing standing between a user
and a module's authority**, and consent is obtained over what the manifest asks
for. A Platform that silently drops an ask it cannot read shows the user a
shorter list than the one they are agreeing to.

**Whether the manifest says what kind of thing a module is.**
platform#94 established the gateway as a distinct kind — invoked from outside
rather than by the Platform, with a surface shaped for being called. Nothing in
the manifest distinguishes one.

## Decision

**A manifest names exactly one capability, and module and capability are the same
thing.** One binary, one capability, one manifest, one id. It is what the code
already does, and it gives platform#85's grants, platform#92's quota and
platform#94's prefix a subject that exists rather than one that has to be
invented. A module filling several roles is already expressible — `Provides` is a
list. A module wanting two different sets of authority is two modules, which is
also the only honest granularity: the process boundary is the binary, so
per-capability authority inside one process would be a claim nothing enforces.
`Manifest.ID`'s doc comment is corrected to say so rather than left to imply a
distinction that has never existed.

**The manifest separates what a module offers from what it asks for, and the two
have opposite forward-compatibility rules.** An *offer* is something the module
can do: a role, a verb, a subscription, a schedule, a media type, its
description. An *ask* is something it wants from the Platform: a grant, a storage
quota, a path prefix, egress. Asks are collected under one object; offers stay
where they are.

- **An unknown offer is ignored.** A Platform that does not understand it simply
  never uses it, and the module works minus that one thing. Degradation, and the
  module chose to publish it knowing older Platforms exist.
- **An unknown ask refuses the install**, naming the key it could not read. Not
  because ignoring it is unsafe in itself — the Platform grants nothing it does
  not know about, so nothing escalates — but because the failure is invisible in
  both directions: the module runs believing it was granted what it declared, and
  the user consented to a list the Platform could not fully show them.

Mechanically that is one change in one function: `ParseManifest` stops calling
`DisallowUnknownFields` at the top level and starts calling it on the asks object
alone. Both the Platform's installer and `modulesign` get the new rule by calling
the function they already call, so a manifest that would be refused at install is
refused at signing time instead of reaching a user.

**The `schema` string stays, and stays strict.** It is now for a *breaking*
change to the format — a field whose meaning changed, a structure that moved —
and refusing an unrecognised one is still right. What it stops being is the
mechanism for ordinary growth, which is what it was being asked to be.

**A module declares its kind: `provider` or `gateway`.** The two have different
surfaces and different rules, and the Platform should know which it holds before
it invokes anything. It also lets consent say what sort of thing is being
installed, which is the difference between "this fetches metadata" and "this
answers requests from your network". Kind is an *offer* under the rule above, and
that is safe rather than lax: a gateway must declare a path prefix, which is an
ask, so a Platform too old to know what a gateway is refuses on the prefix before
the kind matters.

**A module declares the media types it sources.** This is what platform#11's
`media_types` registry has been waiting on. The vocabulary is open and
canonicalised on write, so a declaration is a claim rather than a constraint, and
it lets the Platform answer "what can this install source?" without invoking
every module to find out. It is an offer: an older Platform ignoring it loses a
filter hint and nothing else.

## Alternatives considered

**A module wraps several capabilities, with a module level above them.**
*Rejected:* it reopens what platform#85 settled. Grants would need a level to
attach to, and since the process boundary is still the binary, capability-level
granularity would be honest on paper and not in fact.

**Ignore every unknown field.** *Rejected:* maximally compatible and it fails
open on exactly the fields that matter. A module runs without the authority it
declared, and the first symptom is a denied write rather than a refused install.

**Refuse every unknown field** — what the code does today. *Rejected:* it makes
the manifest unable to grow, which is not a theoretical cost with six additions
already recorded.

**Infer the kind from the declarations** — a module with a path prefix is a
gateway. *Rejected:* kind becomes a side effect of a field rather than a
statement, so a malformed manifest changes what a module *is* rather than
failing.

**Leave media types until something needs them.** *Rejected:* this is the change
where the manifest grows, and deferring it buys a second compatibility event to
save one field.

**Generate the manifest from a schema, as `contracts` generates its surface.**
*Rejected without asking:* the SDK is deliberately hand-written Go and
`contracts` is the generated repository. A schema-first manifest would import one
repository's conventions into the other, whose whole identity is Go interfaces a
module implements in its own process.

## Consequences

The manifest can now grow without a coordinated fleet release, which is the
property it did not have. An offer added today reaches older Platforms as
silence; an ask added today refuses them loudly, which is the outcome worth
having when authority is what is being described.

**"Offer or ask" becomes a decision every future field carries**, and it is a
judgement rather than a type. Getting it wrong in the safe direction costs a
refused install; getting it wrong in the other direction reintroduces exactly the
silent-drop failure this record exists to prevent. The asks object is the whole
mechanism, so a field's placement in it *is* the decision, visible in the
document rather than in a comment.

The refusal is only as good as its message. "Unknown ask" naming the key and the
Platform version is a user telling an author to publish a build for an older
Platform; an unexplained refusal is a user concluding the module is broken.

Module and capability being one thing is now a rule rather than an accident, so
the day something wants two capabilities in one distribution it is a decision to
revisit this record, not a small change to a struct.

Nothing here is built. `ParseManifest`, its two callers and the SDK `Manifest`
move together, and no manifest may carry an ask until a Platform that can read
asks is the oldest one in the field.
