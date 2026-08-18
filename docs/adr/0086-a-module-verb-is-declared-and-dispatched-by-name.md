# A module verb is declared in its manifest and dispatched by name

**Status:** Accepted. **Not built.** Depends on
[platform#85](0085-a-modules-authority-is-declared-and-consented.md), which
settled who may hold an action and how it is granted but not how one reaches a
module. Slice 2 of the extension surface.

## Context

`dispatch` in `internal/transport/session` is a closed switch — twenty cases and
a `default` returning `InvalidArgument: unknown action`. It is the complete
enumeration of what any client can invoke, and every arm of it is
Platform-authored. That is precisely what the extension surface names as a module
being unable to *act*: a third party can add a source, but not a verb.

Three things already exist and shape the answer.

The SDUI vocabulary has an `invoke` action kind that carries an arbitrary action
name, so a screen can already trigger a verb the Platform has never heard of
without any contract change. Nothing has to grow on the client side for a module
verb to be reachable.

[platform#85](0085-a-modules-authority-is-declared-and-consented.md) settled the
authority half: a module declares what it needs in a signed manifest, an operator
consents at install, and an action only a module could hold takes a `module.`
prefix that role creation refuses to grant to a person. It deliberately did not
say how an action is declared, addressed, validated or routed.

`ModuleSettingsStore` is `Get(ctx, moduleID)` — one document per module, returning
`{}` for first use. A verb acting on somebody's third-party account needs settings
per person, and that store has no user dimension.

## Decision

**A verb is addressed as `module.<id>.<verb>` and routed from `dispatch`'s
default branch.** The switch gains one arm rather than one per verb. The valid
set is read from the installed manifest, so an undeclared verb is refused by name
before a module process is woken — the default branch keeps refusing unknown
actions, it simply consults the installed set before it does.

**The module id is part of the public action name.** A trace, a screen's action
payload and an error message stay readable without a lookup, which is the
debugging surface this system leans on hardest. The cost is accepted: renaming a
module breaks any screen that named its verbs, and a module id is therefore part
of its public interface rather than an implementation detail.

**A verb declares its input in the manifest, and the Platform validates before
invoking.** Bad input is refused without waking a module process, and a declared
input can generate its own form rather than each module authoring one.

**That declaration is expressed in the contract's existing field and validator
vocabulary, not a new schema language.** The validator set is closed and
generated, and a second validation language would be a second closed set to keep
in step with the first. A verb needing a validator the contract does not have is
a contract change with its own record — the same answer a screen gets when the
vocabulary cannot express it.

**Module settings gain a user dimension on the existing store**, keyed by module
id and user id, where a null user means install-wide. The document that exists
today keeps its meaning as the null-user row, so nothing already stored moves. A
second store was rejected: two stores answering one question is how a module
comes to ask the wrong one and receive a plausible empty document.

**A module never keys settings by user itself.** Identity is wholly
Platform-owned, and a module holding an identity mapping is the one shape that
would make it an identity store.

**A verb always has an invoking user.** There is no system-principal verb in this
slice. Module-declared schedules are slice 3's subject, and deciding them here
would decide them twice — in the smaller slice, and with the strongest authority
in the system.

**Both gates apply.** The invoking user must hold the action, per
[platform#13](0013-how-a-capability-acts.md), and the module must hold whatever
authority the verb exercises, per
[platform#85](0085-a-modules-authority-is-declared-and-consented.md). A verb is
not a way for a user to borrow a module's authority, nor for a module to borrow a
user's. A denial answers `PermissionDenied` and the module sees it.

## Alternatives considered

**An opaque verb id minted at install.** Nothing breaks on rename and a module
cannot squat another's namespace. Rejected because a trace and an action payload
stop being readable without a lookup, and the rename it protects against is rare
while reading a trace is constant.

**Keeping `dispatch` closed, with the Platform authoring a case per verb.**
Every action stays reviewed. Rejected because it is the constraint this milestone
exists to remove: a third party could still not add a verb without a Platform
change.

**The module validating its own input, with the Platform passing bytes through.**
It matches how module settings already work and adds nothing to the manifest.
Rejected because every module then reimplements validation, a bad input costs a
process invocation to discover, and no form can be generated from a shape nobody
declared.

**A system-principal verb in this slice.** A scheduled scrobble would become
possible immediately on authority
[platform#85](0085-a-modules-authority-is-declared-and-consented.md) already
defines. Rejected as front-running slice 3, which owns schedules and will have to
decide the same question more carefully.

## Consequences

- **`dispatch` stops being the complete enumeration of what a client can
  invoke.** The complete list becomes its cases plus the installed manifests, and
  any document stating a fixed count of actions becomes wrong the moment a module
  with a verb is installed.
- **A module id is public interface.** Renaming one is a breaking change for
  screens that named its verbs, which is a constraint the registry's catalogue
  does not currently express.
- **The manifest grows a declaration bounded by somebody else's closed set.** A
  verb wanting a validator the contract lacks is blocked on a contract release,
  not on a manifest edit — the same trade the screens already make.
- **The settings migration must preserve the existing document as the null-user
  row**, or every module with settings appears unconfigured on upgrade.
- **A verb cannot run without a user until slice 3 lands.** A module whose whole
  value is periodic work has no path yet, and that is a stated gap rather than an
  oversight.
