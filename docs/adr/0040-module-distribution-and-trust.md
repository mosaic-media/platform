# Module distribution and trust: signed binaries and user-added repositories

**Status:** Proposed; built in part. Partly superseded: the **actor** is reversed
by [platform#49](0049-the-platform-manages-extension-modules.md) — the Platform, not
the Supervisor, verifies and installs extension modules — and the distribution,
signing and trust *mechanism* below stands unchanged. The **verification gate**
and the **signed-repository install flow** are built (Platform-side): a module's
signed manifest, its declared digest and its SDK major are checked before it is
run; a repository is a signed index over HTTPS trusted per repository, and the
Platform fetches, verifies, stores and keeps provenance for a module installed
from one. `tools/modulesign` is the publisher side (sign, sign-index). Not built:
the admin surface that collects a user's consent to add a repository, real key
custody for the official key, and the composition-root wiring to a live official
repository — the machinery exists; a running Platform does not yet install from a
real repository. **Key custody is decided by
[platform#76](0076-the-signing-key-hierarchy.md)** and still unbuilt: two keys held
offline as well as in CI, rotated by overlapping trust, with revocation still
open.
**Date:** 2026-07-22

Depends on [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md),
[platform#38](0038-platform-binary-built-by-ci.md) and
[platform#39](0039-extension-module-boundary.md). Begins to discharge what
[platform#4](0004-static-go-module-composition.md) left as "signing and trust
tiers". Nothing here is built.

## Context

Distribution used to be a Go problem. Under
[platform#4](0004-static-go-module-composition.md), a module was source resolved
from the Go module proxy, and its integrity was the proxy's checksum database.
Under [platform#38](0038-platform-binary-built-by-ci.md) and
[platform#39](0039-extension-module-boundary.md) it is a **binary someone
downloads and executes**, and the Go toolchain's guarantees are no longer in the
picture.

Two things change with it.

**Trust moves from source to artefact.** A build pipeline compiled from source
the user's machine could in principle inspect and that the checksum database
pinned. A prebuilt third-party binary cannot be inspected by anyone who receives
it. Nothing about the process boundary
([platform#39](0039-extension-module-boundary.md)) changes this: a separate process
still runs with the filesystem, network and privileges of its user.

**Someone has to decide what a user is allowed to install.** A closed set makes
Mosaic a curated product and forfeits the ecosystem the SDK exists to enable
([sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md)). An open set with no
distinction means the first malicious module is indistinguishable from the
official ones. Jellyfin's plugin repositories are the worked example of the
middle path, and the shape it settled on — an official repository trusted by
default, third-party repositories addable with an explicit warning — is what this
record adopts.

## Decision

**Every installable artefact is signed. Mosaic's own repository is trusted by
default. A user may add third-party repositories, with explicit informed consent,
and the provenance of what they installed stays visible afterwards.**

### Signing is universal; trust is about whose key

- **The Platform binary is signed by Mosaic and verified by the Supervisor before
  activation.** [platform#38](0038-platform-binary-built-by-ci.md) makes the
  Supervisor a downloader, and an unverified download is not an improvement on a
  local build.
- **Every extension module binary is signed**, whoever publishes it. Signing is
  not a mark of endorsement; it is what makes "this artefact is the one that
  publisher released" checkable at all. An unsigned artefact is refused
  regardless of origin.
- **The Supervisor verifies the signature and the manifest's declared digest
  before an extension module is ever executed**, and refuses on mismatch.

### A repository is a signed index

An extension module ships as a **binary plus a manifest**, and a repository is a
signed index over HTTPS listing them. The manifest declares:

- module id, module version, and human-readable name;
- the **SDK major version** it was built against
  ([platform#39](0039-extension-module-boundary.md));
- the provider roles it fills
  ([sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md));
- the platform/arch of each binary, and each binary's digest.

**The manifest is readable without executing the binary**, which carries
[supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)'s standing rule into a
world where the artefact is executable rather than source. That rule mattered when
it prevented source analysis; it matters more now, because the alternative to
reading a manifest would be running an unverified binary to ask it what it is.

### Mosaic's repository is trusted; others are added at the user's risk

- **Mosaic's official repository is configured and trusted by default.** A module
  from it has been through whatever review the project applies, and a user who
  never touches repository settings only ever installs from it.
- **A user may add a third-party repository.** Doing so requires explicit consent
  in the admin surface, presented as what it is: modules from this repository are
  not reviewed by Mosaic, they run with the Platform's authority, and installing
  one is a decision the user is making about code they have not read.
- **Consent is per repository, not per module and not global.** Adding a
  repository is the moment the user takes on the risk; making them re-consent per
  module trains them to click through, and a single global "allow untrusted"
  toggle detaches the decision from the thing being decided.

### Provenance stays visible after install

**A module's origin is shown wherever the module is, not only at the moment it is
added.** In the module list, in its settings page
([sdk#4](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0004-module-contributed-settings-ui.md)), and in whatever the
administrator sees when something goes wrong.

This is the part that is easy to skip and is most of the value. A consent dialog
clicked six months ago is not context when a user is looking at a broken import or
an expert-mode trace ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md))
and deciding what to suspect. The disclaimer is a moment; the provenance is a
property, and it should be rendered like one.

### The release matrix is cheaper than it looks, with one trap

Go cross-compiles to every supported target from any host with `GOOS`/`GOARCH` and
no cross-toolchain, so producing linux/amd64, linux/arm64, darwin and windows
binaries is a loop, not a build farm. The tooling
([platform#5](0005-developer-platform-toolchain.md)) should emit all targets in one
command so a module author never thinks about it.

**The trap is cgo.** A cgo dependency requires a C cross-toolchain per target and
turns a loop into a matrix of build environments. This is a live concern precisely
because [platform#39](0039-extension-module-boundary.md) makes a module-local SQLite
store possible, and the obvious SQLite driver (`mattn/go-sqlite3`) needs cgo while
a pure-Go one (`modernc.org/sqlite`) does not. **The module template and
documentation should prefer pure-Go dependencies and say why**, so the arm64 NAS
case stays a first-class one rather than becoming the platform nobody ships for.

## Alternatives considered

**Mosaic-signed only; no third-party repositories.** *Rejected.* It is the
strongest security posture and it makes Mosaic a curated product with a
contribution queue rather than an ecosystem — forfeiting what
[sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) exists for, and what
[platform#2](0002-module-storage-and-delivery-model.md) §3 was trying to protect
when it insisted community modules not be second-class.

**Any URL, no repositories, no signing.** *Rejected.* Maximum freedom and no way
to tell a compromised artefact from an intended one, no revocation story, and no
provenance to render after the fact.

**Trust tiers with graduated capability — untrusted modules get fewer
permissions.** *Deferred, not rejected.* It is the right end state and it is
unbuildable today: module-granular permissions do not exist
([platform#13](0013-how-a-capability-acts.md) reserves them), so there is no
narrower authority to grant. Shipping tiers that all resolve to full authority
would be describing containment that does not exist, which
`docs/index.md` explicitly forbids. When permissions land, this is where they
attach.

**Mosaic's CI builds community modules from source.** *Rejected.* It would give
every module a uniform, verifiable release matrix and a real provenance chain —
and it puts Mosaic in the position of running arbitrary third-party build scripts,
makes the project the bottleneck for every community release, and reintroduces the
build pipeline [platform#38](0038-platform-binary-built-by-ci.md) removed, centrally
rather than per install.

**Per-module consent instead of per-repository.** *Rejected*, as argued above: it
trains click-through and misattributes the decision.

## Consequences

- **A user can install a module Mosaic has never seen**, which is the ecosystem
  working. They will have been told plainly what that means, once, at the point of
  decision, and reminded by the provenance shown alongside the module thereafter.
- **Signing is now infrastructure Mosaic must operate** — key custody, the signing
  step in CI, and eventually rotation. This is real ongoing work that did not exist
  when the Go module proxy was doing it.
- **Trust decided at install is the *main* control, not the only one any more.**
  [platform#39](0039-extension-module-boundary.md) contains egress — every host a
  module contacts is proxied, attributed and deny-listable, and on Linux the
  process cannot dial out at all. That is one axis. The filesystem, the authority
  a module is handed, and what it does with it are still governed by nothing but
  the install decision.
- **`docs/index.md`'s statement that installing a community module means running
  arbitrary code with Platform authority remains true**, and this record holds
  most of the mitigation: informed consent and durable provenance rather than
  containment. The line to hold in user-facing text is that a module's *network
  reach* is controlled and its *authority* is not — stating either half alone
  would mislead.
- **Third-party modules are not in the Go module proxy's integrity story**, so
  the checksum database no longer protects them. The signature and the manifest
  digest replace it, and they are only as good as key custody.

**Open.**

- **Revocation.** What happens when a signing key is compromised or a published
  module is found malicious. There is no mechanism here, and a repository index is
  the natural place for one.
- **Discovery.** A catalogue, search, ratings, install counts — none of it is
  decided. This record covers how a module is *obtained and trusted*, not how it is
  *found*.
- **What review of the official repository actually means.** "Trusted by default"
  is a promise about a process that does not exist yet, and it should be written
  down before it is made to users.
- **Module-granular permissions**, per the deferred alternative above.
