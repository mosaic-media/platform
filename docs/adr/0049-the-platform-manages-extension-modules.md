# The Platform manages extension modules; the Supervisor manages the binary

**Status:** Accepted; **built, except the third-party repository surface.** The
division landed as decided: the Platform discovers, downloads, verifies,
installs, pins, spawns, health-checks, restarts and kills an extension module
(`internal/adapters/extension`), and the Supervisor touches none of it — it
manages the Platform binary and through it the core modules, and says so in its
own source. The install and uninstall actions are Platform client surfaces
([platform#37](0037-one-client-transport.md)), shaped by
[platform#51](0051-extension-installation-is-user-initiated-and-persistent.md).
**Unbuilt: adding or consenting to a third-party repository.** The trusted set
is Mosaic's own key compiled into the binary, plus the development override
[platform#55](0055-the-development-module-repository.md) adds; `Registry.Add`
exists and no client action reaches it, so the per-repository trust decision
[platform#40](0040-module-distribution-and-trust.md) puts at the centre of the
model is one only a build can make.
**Date:** 2026-07-24

Partly supersedes [platform#39](0039-extension-module-boundary.md) (the "Supervisor
owns the install" division) and [platform#40](0040-module-distribution-and-trust.md)
(the Supervisor as the verifier and installer of extension modules). Leaves
[platform#38](0038-platform-binary-built-by-ci.md) intact. Nothing here is built.

## Context

[platform#39](0039-extension-module-boundary.md) split the handling of an extension
module across two processes. Under its "The Platform owns the process; the
Supervisor owns the install" section, the **Supervisor** installs, verifies,
pins versions and holds the selected set, then hands the **Platform** a list of
installed modules and their binaries; the Platform spawns, health-checks,
restarts and kills. [platform#40](0040-module-distribution-and-trust.md) put the
signature and digest verification on the Supervisor too — it "verifies the
signature and the manifest's declared digest before an extension module is ever
executed."

That split has not survived thinking about what each process is *for*.

**It re-introduces the coupling the tier split exists to break.** [platform#39](0039-extension-module-boundary.md)'s own
argument for keeping the running module out of the Supervisor is that "routing
restarts through the Supervisor would make a wedged third-party process a
host-lifecycle concern." Installation is the same concern one step earlier: a
third-party module that fails to download, fails to verify, or must be
re-fetched is a dynamic, failure-prone, third-party matter, and putting it in
the Supervisor makes the durable host-lifecycle layer depend on the health of
the least trustworthy thing in the system.

**It splits one operation across a process boundary for no benefit.** Verifying
a signature and then spawning the verified binary is naturally one act. [platform#39](0039-extension-module-boundary.md)
had the Supervisor verify and the Platform spawn, which means the Platform must
either trust that the Supervisor verified (and re-verification is the safer
posture for the process that grants the module its authority) or verify again.
The process that spawns a module and hands it authority is the right process to
have checked it.

**It makes the Supervisor larger, against its whole design.**
[supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md) wants the Supervisor small and
dependable. An extension ecosystem is the opposite: repositories, downloads,
signatures, user-added trust, revocation. Every one of those in the Supervisor
is surface in the component whose job is to still be working when everything
else is not.

The clean line is by tier. Core modules are the binary; extension modules are
the dynamic ecosystem.

## Decision

**The Platform manages extension modules end to end. The Supervisor manages the
Platform binary — and through it, the core modules — and nothing else about
modules.**

### The Platform owns the whole extension lifecycle

Discovery, download, signature and digest verification, install, version
pinning, spawn, health, restart, kill. An extension module is a runtime concern
from the moment it is chosen to the moment it is removed, and the runtime owns
it throughout. [platform#40](0040-module-distribution-and-trust.md)'s mechanism is
unchanged — every artefact is signed, an unsigned one is refused, Mosaic's
repository is trusted by default, a user may add third-party repositories with
explicit informed consent, and provenance stays visible after install — but the
**actor is the Platform**, not the Supervisor. The admin surface where a user
adds a repository or consents to it is a Platform surface, served like every
other client surface ([platform#37](0037-one-client-transport.md)).

### The Supervisor owns the binary, and that is the core modules

[platform#38](0038-platform-binary-built-by-ci.md) is untouched and is where the
Supervisor's module responsibility now begins and ends: it downloads the signed
Platform binary, verifies *that one artefact*, activates it, and activates a new
Generation when the core-module selection changes. Core modules ride inside that
binary, so managing the binary is managing the core modules. The Supervisor
never sees an extension module — not its bytes, not its signature, not its
process.

### What stays as [platform#39](0039-extension-module-boundary.md) already had it

The Platform already spawns, health-checks, restarts and kills — that half of
[platform#39](0039-extension-module-boundary.md)'s division was right and is built. This record extends the same
ownership backwards over install and verification, so the boundary between the
two processes falls cleanly between *core* and *extension* rather than through
the middle of a single extension module's life.

## Alternatives considered

**Keep [platform#39](0039-extension-module-boundary.md)/0065's split — Supervisor installs, Platform runs.** *Rejected*,
on the three grounds in Context. It is a defensible division and it is what is
written down; its appeal is that the durable layer holds the trust decisions.
But it makes the durable layer depend on the least durable thing, splits
verify-then-spawn in two, and grows the component that must not grow.

**The Supervisor installs core *and* extension; the Platform only runs.**
*Rejected.* It is the most consistent "Supervisor owns all installation" line,
and it is the wrong consistency: it maximises exactly the coupling and the
Supervisor surface this record removes.

**The Platform manages everything, including its own binary.** *Rejected.* A
process cannot dependably replace the binary it is running from; that is the
whole reason the Supervisor exists ([supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md)). The binary is the one artefact
that must be managed from outside the Platform, which is precisely why the line
is drawn there.

## Consequences

- **The Supervisor shrinks to one artefact.** It downloads, verifies and
  activates the Platform binary and manages Generations. The entire extension
  repository, signing-verification and trust surface leaves it. This is the
  largest simplification of the Supervisor since [platform#38](0038-platform-binary-built-by-ci.md) removed the build
  pipeline.
- **`internal/adapters/extension` is where extension management lives, and it
  grows.** It already spawns and supervises; it gains download, verification and
  the repository/trust surface ([platform#40](0040-module-distribution-and-trust.md)'s mechanism, Platform-side). The
  comments in that package that attribute install and verification to the
  Supervisor are now wrong and are corrected in the same change as this record.
- **The stremio cutover ([platform#16](0016-optional-module-composition.md)'s named
  debt) no longer waits on the Supervisor.** It waits on the Platform's own
  install-and-verify path, which is buildable now rather than blocked on a
  component that does not exist. The runtime egress and lifecycle it also needed
  are built.
- **Verification happens once, in the process that grants authority.** The
  Platform verifies a module's signature and digest immediately before spawning
  it and handing it a Caller, which is the safer posture than trusting a check
  another process made earlier.
- **The Supervisor's dependability argument gets stronger, not weaker.** Every
  third-party failure mode — a bad download, a revoked key, a malicious artefact
  — is now contained in the Platform, where a failure degrades a capability. None
  of it can wedge the Supervisor.
- **[platform#40](0040-module-distribution-and-trust.md)'s open questions become the Platform's.** Revocation, discovery, and
  what "trusted by default" means as a review process are unchanged as questions;
  they are answered in the Platform now rather than the Supervisor.
