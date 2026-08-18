# Filesystem containment is applied where the OS allows it, and reported where it does not

**Status:** Accepted. **Not built.** Gives
[platform#92](0092-module-storage-is-granted-not-enforced.md)'s grant boundary a
mechanism on Linux. Does not supersede
[platform#50](0050-deployment-topologies.md) — it identifies one case its
conclusion does not cover.

## Context

[platform#92](0092-module-storage-is-granted-not-enforced.md) grants a module
bounded storage and says it reads and writes there "and nowhere else". That
sentence has no mechanism behind it. The record is careful to say containment is
reported rather than enforced, but the grant boundary is the one place where an
unenforced rule could be read as a guarantee.

[platform#50](0050-deployment-topologies.md) concluded that OS containment
mechanisms need privileges a non-root Platform does not have — a network
namespace, a dedicated uid with a firewall owner rule, seccomp on `connect(2)` —
and that on macOS and Windows there is no low-cost mechanism at all. That
conclusion is right for the mechanisms it surveyed, all of which are about
network egress.

**Today's containment is declared, not verified.** `EgressContainment` is
`{Enforced bool, Detail string}`, set from the `MOSAIC_MODULE_EGRESS`
environment variable. The deployment asserts that it has contained the module and
the Platform relays the assertion. That is honest, and it is as far as the
Platform can go for egress.

A full sandbox — a WASM runtime or an embedded scripting VM — was considered and
is addressed under Alternatives, because it does not fail on cost. It fails on
what it would break.

## Decision

**Landlock is applied to every extension module process at launch, on Linux.** It
is the exception to [platform#50](0050-deployment-topologies.md)'s conclusion:
filesystem-scoped, available from Linux 5.13, and **unprivileged by design**,
which is precisely the objection that defeated the mechanisms that record
surveyed. The module is restricted to its own storage grant plus what it needs to
execute, and nothing else.

**This is a different kind of posture from the egress one, and the difference is
recorded rather than blurred.** Egress containment is *declared* by the
deployment and relayed. Filesystem containment is *applied* by the Platform, so
the Platform knows whether it succeeded and reports a fact rather than a claim.
Two fields that both read "Enforced" would otherwise imply the same standard of
evidence.

**Where no unprivileged mechanism exists, the posture is reported, per
platform, and not claimed once.** macOS and Windows keep a reported posture. The
guarantee is uneven, and describing it as uniform would be the failure this whole
line of records exists to avoid.

**If applying Landlock fails at launch — an older kernel, an ABI mismatch — the
module still launches and the posture downgrades to reported.** Refusing would
make a kernel version decide whether a person's modules work, and the honest
report is what the posture field is for.

**An install is not refused for want of enforced containment. The posture is
shown at the consent step** ([platform#85](0085-a-modules-authority-is-declared-and-consented.md)),
where a human is already deciding whether to trust this binary, and it says what
containment this deployment actually has. Refusing without enforcement would
remove extension modules from macOS and Windows — most desktop self-hosters —
and the extension surface is the thesis of the milestone that would be defending.

**Core modules are not contained, and that asymmetry is the tier.** They are
linked into the binary, so there is no process to contain and no boundary to
enforce; trust is established before the build rather than at runtime. Sandboxing
applies to what is installed at runtime because that is what nobody reviewed
before shipping.

**Landlock's network rules are noted and not adopted here.** ABI v4, from Linux
6.7, can restrict TCP bind and connect. That would strengthen egress containment
from declared toward applied on new-enough kernels, and it is a separate decision
with its own tradeoffs about the forward proxy.

## Alternatives considered

**A full sandbox: extension modules as WASM or scripts.** Genuine capability
control — no filesystem, no network, no syscall except what the host hands over.
Rejected, and not on cost. **It would split one module system into two.** A WASM
module cannot be compiled into the Platform binary as a Go library, so core and
extension modules would stop being the same thing, and "a module moves between
tiers as a build change rather than a rewrite" —
[platform#39](0039-extension-module-boundary.md),
[platform#48](0048-core-modules-keep-their-repositories.md), and
`module-cinemeta`'s own boundary test — would no longer be true. It would also
change what the SDK *is*: Go interfaces a module implements become a host-function
ABI. And it lands hardest on the cases
[platform#92](0092-module-storage-is-granted-not-enforced.md) just decided, where
throughput is the point.

**Refusing to install extension modules without enforced containment.** The
guarantee would then be real everywhere it is claimed. Rejected: it removes the
extension surface from the platforms most self-hosters run.

**An operator setting that refuses by default and can be turned off.** Rejected
as a consent dialog wearing a configuration file's clothes — it would be enabled
immediately and understood rarely.

**Leaving containment entirely to the deployment.** Consistent with what is
already recorded, and no platform-specific code to carry. Rejected because
Landlock costs little and turns
[platform#92](0092-module-storage-is-granted-not-enforced.md)'s grant boundary
from a convention into a mechanism on the majority platform.

## Consequences

- **The guarantee is uneven and must always be described per platform.** Any
  sentence claiming Mosaic contains its modules is wrong without naming the
  operating system.
- **A second platform-specific path enters the extension host**, which until now
  had none of consequence. It needs a test that runs on Linux and a defined
  behaviour everywhere else.
- **The grant boundary becomes real on Linux and stays a convention elsewhere**,
  so a module misbehaving that way is caught on one platform and invisible on the
  others — which is a worse debugging story than either extreme.
- **Per-module resource observation still does not exist.**
  [platform#92](0092-module-storage-is-granted-not-enforced.md)'s gap is
  untouched: nothing here counts RSS, CPU or disk, and Landlock does not.
- **The consent step gains something a person must understand to act on.**
  Showing a containment posture is only useful if it is phrased for somebody who
  does not know what a Landlock ABI is.
