# A module updates itself until it asks for more

**Status:** Accepted. **Not built.** Answers the update half of
[platform#51](0051-extension-installation-is-user-initiated-and-persistent.md)'s
open pair; the revocation half is
[platform#99](0099-revocation-is-a-signed-list-checked-on-a-schedule.md).
**Depends on** [platform#97](0097-a-manifest-names-one-capability-and-separates-asks-from-offers.md),
without which the condition below cannot be evaluated at all.

## Context

platform#51 lists three candidates for updating an installed extension — a user
action, an auto-update policy, or neither — and picks none.

Stated as a straight choice it is a bad trade in both directions. A user action
means a module with a security fix waits for somebody to open a settings screen,
which most people never do. Blanket auto-update means a module silently becomes a
different module, and under
[platform#85](0085-a-modules-authority-is-declared-and-consented.md) consent is
the only thing standing between a user and a module's authority — so an update
that quietly acquired a grant would be authority obtained without consent.

What changes the shape is platform#97. Before it, "did this version want more
than the one I consented to?" was a question nobody could ask a manifest: the
declarations were scattered and there was no way to tell one that requests
authority from one that merely describes. platform#97 collects the **asks** under
one object precisely so a Platform can enumerate them. That makes the question
mechanical.

## Decision

**A module updates itself automatically when both hold: the new version is not a
major bump, and its asks have not grown. Otherwise the update is offered to a
person and nothing happens until they accept.**

The two conditions are doing different jobs and both are needed.

**Asks have not grown** is the consent condition. It is *grown*, not *changed*: a
version that asks for **less** is strictly safer than the one already consented
to, and blocking it would leave people on a more privileged version out of
caution, which is the wrong way round. Growth is what re-opens consent, because
growth is what a user was never asked about.

**Not a major bump** is the behaviour condition. Asks cover authority and say
nothing about what a module does with it — a module can hold exactly the same
grants and change what it returns, what it names things, or what it stores.
Semantic versioning is the author's own statement that this release is not a
drop-in replacement, and taking them at their word is cheaper and more honest than
inferring it. This is a different check from the SDK-major compatibility
[platform#39](0039-extension-module-boundary.md) already makes, which refuses an
incompatible module before its process starts; this one is about the module's own
version.

**A blocked update is an Issue, not a silent hold.** It names the version, and
what the new one asks for that the installed one does not, so the decision a
person is being asked to make is the same decision they made at install —
[platform#74](0074-operational-findings-are-durable-state.md)'s register already
being where a Platform says what a person should do.

**Automatic updating is configuration**, with a reload class like every other
field, so an operator who wants nothing to move without being asked can have that.
The default is on, because the population this protects is the one that never
opens a settings screen.

## Alternatives considered

**A user action for every update.** *Rejected:* correct and unusable. The people
most exposed are the least likely to press the button.

**Blanket auto-update with an opt-out.** *Rejected:* it makes consent decorative.
An update is exactly when a module's asks can change, so a policy that ignores
them is a policy that ignores the one event it exists to cover.

**Neither — reinstall is the update.** *Rejected:* uninstalling drops the install
record and its settings, so "update" would mean "reconfigure from scratch".

**Infer a behaviour change by comparing manifests rather than trusting the
version.** *Rejected:* the manifest describes authority and shape, not behaviour.
Two versions with identical manifests can differ completely, so the comparison
would report safe on exactly the change it was added to catch.

## Consequences

**A module can add an offer and update itself into it.** A new role or verb means
the module starts answering something it did not before, with no new authority.
That is deliberate — it is the shape of an ordinary feature release — but it means
"auto-updated" does not mean "does the same thing", and the update ought to be
visible in the register even when it needed no decision.

**Consent becomes a diff rather than a list.** The install prompt shows what a
module asks for; the update prompt has to show what is *new*, or a person re-reads
the whole list and learns nothing about what changed. Those are different screens
built from the same data.

**A module author's version discipline now has teeth.** An author who never bumps
a major gets silent updates forever, and one who bumps needlessly trains people to
click through prompts. Neither is enforceable from here, which is worth saying
plainly rather than assuming good behaviour.

**This is the first thing that makes a module's version number load-bearing at
runtime.** It was previously read from the build graph for reporting; it now
decides whether something happens without asking.
