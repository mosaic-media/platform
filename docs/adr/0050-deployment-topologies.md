# Deployment topologies: a native binary that runs in a container or on bare metal

**Status:** Accepted. Partly built: the reportable egress-containment posture
(roadmap slice 5.2) — the Platform determines its posture per this table and
reports it at boot and in the support bundle, so the layer-3 guarantee is stated
per deployment rather than claimed uniformly. The OS-level enforcement mechanisms
themselves remain deployment-provided.
**Date:** 2026-07-24

Consolidates a decision scattered across [supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md)
(Docker Compose framing), [platform#38](0038-platform-binary-built-by-ci.md) (install
is downloading a binary) and [platform#39](0039-extension-module-boundary.md) (layer-3
egress is "deployment-dependent"). Names the supported install topologies and,
because it is the part most easily missed, what security enforcement each one
provides. Nothing here is built beyond what those records already cover.

## Context

Two things about how Mosaic runs have been true but never stated together, and
their separation causes a specific confusion.

**The container the repositories run everything in is a *development* container,
not the deployment.** Every gate runs in `docker-compose.test.yml`, and every
`CLAUDE.md` says "everything runs in the container, nothing runs on the host."
That is a rule about running the *checks* — it exists so a developer's machine
cannot pass a gate a clean one would fail. It says nothing about how a user
installs Mosaic, and reading it as "Mosaic is a container application" is a
natural mistake that this record exists to correct.

**Mosaic is a native binary.** [platform#38](0038-platform-binary-built-by-ci.md)
made installation "downloading a binary" precisely to keep a Go toolchain out of
the install path, and Go cross-compiles to every supported target from one host.
So the Platform (and the Supervisor, when it exists) is an ordinary executable
for linux/amd64, linux/arm64, darwin and windows. A container image is one way
to ship that binary, not what it fundamentally is.

The confusion has a cost beyond tidiness, and it is [platform#39](0039-extension-module-boundary.md)'s
layer 3. Denying an extension-module process a network of its own — the thing
that turns the egress proxy from the easy path into the only path — is done with
a different mechanism on each topology, and on some there is no mechanism at all.
A single "Mosaic runs in a container" assumption would either overpromise that
guarantee on bare metal and macOS or forgo it in the container where it is
strongest. The topology is a security-relevant fact, so it is decided and
written down rather than left to how a particular user happened to deploy.

## Decision

**Mosaic ships as a native binary and supports three install topologies. The
egress-containment guarantee ([platform#39](0039-extension-module-boundary.md)'s
layer 3) is stated per topology rather than uniformly, because it genuinely
differs.**

| Topology | What it is | Layer-3 egress containment |
|---|---|---|
| **Container** (Linux) | The binary in an image, `docker compose up` ([supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md)) | **Strongest.** A module runs in its own network namespace (or its own container) with no interface but the socket mount, so the proxy is the only path — enforced, not convention |
| **Native, Linux** | The binary on the host, systemd or a bare process | **Enforceable.** A dedicated uid with a firewall owner rule, or seccomp on `connect(2)`, denies direct egress; which mechanism depends on privileges the deployment grants |
| **Native, macOS / Windows** | The binary on the host | **Convention.** No equivalent low-cost control, so the proxy is the easy path and not the only one; a module *can* dial out directly ([platform#39](0039-extension-module-boundary.md) says so) |

Three things follow and are worth stating outright.

**The two lower guarantees are still strictly better than what exists without the
proxy at all.** Even where layer 3 is convention, the forward proxy sees and
attributes every host a cooperating module contacts and applies the deny list to
it — and every first-party module is cooperating. What convention does not do is
contain a *hostile* module, which only the container or the Linux native controls
achieve.

**The primary supported target is Linux, container or native**, because that is
where the guarantee is real and it is [supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md)'s
Docker Compose framing. macOS and Windows native are supported for running Mosaic
— a developer's laptop, a home machine — with the containment caveat stated in
the product's own words, not buried.

**A deployment must be able to learn its own topology and say what it enforces.**
The layer-3 guarantee is not a claim the documentation gets to make uniformly; it
is a runtime property the Platform can report, so an admin surface can show
"module egress is enforced" on Linux-in-a-container and "module egress is
attributed but not enforced" on a Mac. Stating either half alone would mislead —
the line to hold, from [platform#40](0040-module-distribution-and-trust.md), is that
a module's network *reach* is controlled where the OS allows and its *authority*
is not.

## Alternatives considered

**Container-only.** *Rejected.* It is the strongest and simplest security story
and it is a real product decision some projects make. It also forfeits the
"download a binary and run it" install [platform#38](0038-platform-binary-built-by-ci.md)
built, turns a home user's single binary into a container-runtime dependency, and
makes a developer's laptop a worse target than it needs to be. Mosaic is
self-hosted media for people who may not run containers.

**Uniform guarantee, claimed everywhere.** *Rejected*, and it is the failure this
record exists to prevent. Promising layer-3 containment on macOS native would be
describing a control that is not there — the exact thing `docs/index.md` forbids.

**A fourth topology: one container per module.** *Named, not a tier.*
[platform#39](0039-extension-module-boundary.md) already offers it as an operator
option — the strongest and most portable form of layer 3 — without the Platform
depending on driving a container runtime. It sits under "Container" above as a
hardening choice, not a separate supported install path.

## Consequences

- **The release matrix is the concrete next step, and it is small.**
  [platform#38](0038-platform-binary-built-by-ci.md)'s CI cross-compiles the one
  Platform binary to every target in a loop. That produces the artefacts every
  topology here installs, and it is buildable now; only signing waits on key
  custody.
- **The container image is a thin wrapper, built from the same binary CI already
  produces.** It is a packaging artefact, not a second build, so a container
  deployment and a native one run identical bytes.
- **The layer-3 slice is really three slices**, one mechanism per enforceable
  topology, plus the runtime self-report. That is why
  [platform#39](0039-extension-module-boundary.md) left the mechanism open "to be
  settled against a real deployment": there is no single mechanism, and this
  record is why.
- **`docs/index.md`'s single-binary vocabulary is now the whole truth**, not a
  container caveat. Installing Mosaic is obtaining one executable; how it is
  supervised and how its modules are contained are separate, topology-dependent
  layers on top.
- **The cgo caveat from [platform#40](0040-module-distribution-and-trust.md) applies
  here too.** A pure-Go binary cross-compiles to every target in the loop; a cgo
  dependency turns the loop into a build matrix and jeopardises exactly the
  arm64-NAS native install this record wants to keep first-class.
