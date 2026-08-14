# The extension module boundary

**Status:** Accepted; built in part. The wire, the harness, the Platform-side
host and the invocation-scoped `Caller` handle are built and exercised against a
module in its own process over a Unix socket. **The callback-chattiness question
this record left open is measured and answered: a callback costs ~700µs, so a
25-call season import spends ~17.5ms crossing the boundary and the coarser
batched verbs are not needed.** Process lifecycle is
built — health probing, restart with backoff, and a crash-loop policy that
disables a module rather than exiting the Platform, which closes the open
crash-loop question below. The egress forward proxy and its deny list are built
too (layer 2): every module's outbound call goes through a per-module CONNECT
proxy applying netguard's deny list, and `sdk/host` forces the module's
transport through it — including for loopback, which `HTTP_PROXY` alone
excludes, the case that would otherwise leave the Platform's own PostgreSQL
reachable. Still unbuilt: OS-level network denial (layer 3), which is what turns
the proxy from the easy path into the only path. **The cutover has since
happened**: `module-stremio-addons` and the other two extension modules are in
neither the Platform's `go.mod` nor its composition root, and are installed at
runtime under
[platform#51](0051-extension-installation-is-user-initiated-and-persistent.md).
**This record supersedes [platform#4](0004-static-go-module-composition.md)'s
rejection of module RPC, for the extension tier only** —
[architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md) keeps that rejection for the core modules,
which are still statically linked.
**Partly superseded: the "Supervisor owns the install" division is reversed by
[platform#49](0049-the-platform-manages-extension-modules.md)** — the Platform
manages extension modules end to end, including download and verification; the
process ownership this record built (spawn, health, restart, kill) stands and is
what that division now extends backwards over install.
**Date:** 2026-07-22

Depends on [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md). Supersedes
[platform#4](0004-static-go-module-composition.md)'s rejection of module RPC for
the extension tier only. Presumes
[contracts#6](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0006-contracts-protobuf-workspace.md). Nothing here is built.

## Context

[architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md) puts extension modules in their own process.
This is how the process is crossed, and the constraint that shapes every part of
it is that **crossing it must not change what a module author writes.**

The published surface is Go interfaces: `Capability` and `ImportRequest`
([platform#15](0015-module-capability-and-invocation.md),
[platform#17](0017-module-settings.md)), seven provider roles
([sdk#2](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0002-modules-as-typed-capability-providers.md)), `ContentService`,
and `Telemetry` ([sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)). Two
existing modules and a reference capability are written against them, and
`test/sdkboundary` pins the rule that a module imports only the SDK. If moving
out of process means rewriting all of that against a generated RPC stub, the
extension tier costs the ecosystem its entire existing surface.

Three properties of the current design constrain the answer sharply:

- **The SDK has no dependencies at all.** Its `go.mod` is a module line and a Go
  version. [sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md) rejected exporting
  the OpenTelemetry API specifically to keep it that way, on the grounds that a
  contract must not distribute the Platform's taste in libraries. A gRPC serving
  harness inside `sdk` would break that property by exactly the mechanism [sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md) refused.
- **The call graph is bidirectional.** The Platform calls `Import` and the
  provider roles; the module calls back into `ContentService` (many times per
  import) and `Telemetry`. This is not a one-way service.
- **`v1.Caller` is opaque and forwarded.** A capability acts as the caller it is
  handed ([platform#13](0013-how-a-capability-acts.md)), and in process that value
  is meaningless outside the invocation. Serialized, it becomes something a
  module *holds*.

## Decision

**The SDK's Go interfaces stay the contract. The wire is an implementation of
them, on both sides, and nothing above the capability registry knows it exists.**

### The process boundary is invisible above the registry

Two adapters, one per side:

- **In the Platform, an extension host** that implements `v1.Capability` and the
  provider interfaces by talking to a process. The `CapabilityRegistry` holds
  `v1.Capability` values and cannot tell which are local structs and which are
  proxies. No Platform code above the registry changes, and neither does
  `ImportContent`, provider resolution, or capability-gated affordances
  ([platform#24](0024-capability-gated-affordances.md)).
- **In the module, a host harness** that serves the wire and dispatches to the
  author's plain Go `Capability`. A module gains a `main.go` of roughly
  `func main() { host.Serve(stremio.New()) }` and otherwise keeps the code it
  has.

So a module can move between tiers as a build change rather than a rewrite, the
existing boundary tests keep their meaning, and `capabilities/reference` and
`test/sdkprobe` remain valid proofs. This is the same discipline
[sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md) applied when it declared a
telemetry interface rather than re-exporting an implementation.

### The SDK splits: contract and harness

**`github.com/mosaic-media/sdk` stays dependency-free.** The harness lives in a
**nested Go module** at `github.com/mosaic-media/sdk/host` — its own `go.mod` in
the same repository, so the two are authored and released together while the
parent's dependency graph stays empty. `host` depends on the generated wire
bindings and on gRPC; `sdk` depends on nothing, as now.

A module author importing only `sdk` gets the contract and can be tested with no
transport at all. Adding `sdk/host` is what makes the module runnable as a
process. The cost is one extra import and a nested-module tagging convention
(`host/v1.2.3` alongside `v1.2.3`), which is a known Go wrinkle rather than a
novel one.

This preserves [sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)'s property intact rather than retiring it, which matters
because that property is what makes the SDK a contract rather than a
distribution.

### The wire lives in `contracts`, generated

The protocol is **gRPC over a Unix domain socket** — a named pipe on Windows. No
port allocation, no accidental network exposure, and filesystem permissions as the
access control. The connection is bidirectional: the Platform calls `Import` and
the provider roles; the module calls back into `ContentService` and `Telemetry`
over the same connection, within the invocation that opened it.

The schema is a third proto module — `proto/mosaic/module/v1/` — in the buf
workspace [contracts#6](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0006-contracts-protobuf-workspace.md) establishes, alongside
`mosaic/sdui/v1` and `mosaic/session/v1`, generated for Go and published under
`github.com/mosaic-media/contracts/gen/…`. **This presumes [contracts#6](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0006-contracts-protobuf-workspace.md) is accepted
and the `sdui` → `contracts` rename has happened**; today the repository is still
`sdui` and `platform` requires `github.com/mosaic-media/sdui`. If [contracts#6](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0006-contracts-protobuf-workspace.md) is not
accepted, this needs a different home, and putting it in `sdk` is not an option
for the dependency reason above.

Schema-generated rather than hand-written, matching how SDUI is already produced:
the `.proto` is the source of truth for the wire, and the Go interfaces remain the
source of truth for the contract. Keeping both honest is a codegen-and-test
obligation, and it is the price of not making the wire the contract.

### Compatibility is the SDK major version

**A module and a Platform are compatible when they share an SDK major version.**
An SDK v1.x module runs against any SDK v1.x Platform; an SDK v2 module against
an SDK v1 Platform is refused. The proto package version tracks the SDK major —
`mosaic.module.v1` is the wire for SDK v1.x — so there is **one number a user
reasons about, not two.** Within a major the wire is additive-only: unknown fields
are ignored and unimplemented roles are reported at handshake rather than
discovered at call time.

**While the SDK is pre-1.0 the compatibility unit is the minor version**, which
is effectively exact pinning. That is correct rather than unfortunate — Go's own
semantics give v0.x no compatibility guarantee, and there are no third-party
authors yet to inconvenience. **Reaching SDK v1.0 is therefore a precondition for
a third-party ecosystem, not for building this tier.** The Stremio module can move
out of process at v0.x under exact pinning; opening the door to outside authors
before v1.0 would be promising a stability that the version number denies.

Declaration and enforcement are separate. The **manifest** declares the SDK major
the module was built against, readable without executing the binary — carrying
forward [supervisor#3](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0003-supervisor-orchestrates-isolated-builds.md)'s standing
rule that manifests are the non-executing source of truth
([platform#40](0040-module-distribution-and-trust.md) owns the manifest). The
**handshake** verifies at connect time that the running binary agrees with what
the manifest claimed and that every declared role is actually served; a mismatch
refuses the connection.

### `Caller` becomes an invocation-scoped handle

The Platform mints a handle when it invokes a module, hands it across as the
`Caller`, and **revokes it when the invocation returns.** The module presents it
on every callback; the Platform resolves it against a table of live invocations
and rejects anything it does not find.

Not a signed token with a TTL: a TTL is a window in which a retained value still
works, and the whole risk introduced by serializing `Caller` is retention. A
handle that stops resolving the instant `Import` returns has no window. The
Platform-side table also gives the boundary somewhere natural to record what a
module did as whose authority
([platform#35](0035-audit-is-a-store-not-a-log-stream.md)).

[platform#13](0013-how-a-capability-acts.md) is otherwise unchanged: a capability
originates no authority and every call it makes re-authorises as the invoking
user.

### Egress is proxied, and blocked at the OS where it can be

The Platform hands in-process modules an `*http.Client` that propagates trace
context and gives the Platform a view of outbound calls
([platform#33](0033-instrument-at-the-seams.md), seam 9). That client cannot cross a
process boundary — but the boundary itself is what finally makes egress
*enforceable*, because an operating system has controls over a process that Go
has none of over a package in its own binary.

**All extension-module egress goes through a forward proxy the Platform operates
on the same socket.** Three layers, doing three different jobs:

1. **`sdk/host`'s HTTP client is pre-wired to the proxy and injects the trace
   context carried over the wire**, so the trace stays continuous through the
   module and out to the third-party service it calls — the property seam 9
   exists for.
2. **The proxy sees every host a module contacts**, attributes it to that module,
   applies a deny list, and can rate-limit. It is a `CONNECT`-style tunnel: the
   Platform sees hosts, **not content**. Terminating a third party's TLS to
   observe a module would be disproportionate, and host-level attribution is what
   is actually needed.
3. **Where the operating system allows it, the module process is given no network
   at all** — no interfaces beyond the socket mount, or a dedicated uid with a
   firewall owner rule, or `connect(2)` blocked by seccomp. Which mechanism is an
   implementation choice; the property is that dialling out directly *fails*, so
   the proxy is the only path rather than the easy one.

**The deny list is the part that matters most, and it closes a hole that is open
today.** An in-process module can open a TCP connection to the Platform's own
PostgreSQL, to anything on the operator's LAN, or to a cloud metadata endpoint at
`169.254.169.254`, and nothing observes or refuses it. The proxy is the first
mechanism in Mosaic that could say no. Loopback, RFC1918 and link-local ranges are
denied by default, with an operator override for the genuine case of a module
sourcing from a service on the local network.

**The traffic this costs is small.** An extension module's egress is JSON: the
Stremio module fetches addon manifests, meta, catalog and stream documents, and
*snapshots* magnet URIs as strings rather than resolving them. Range-probing real
bytes belongs to playback ([platform#29](0029-probing-and-the-per-stream-playback-decision.md)),
which is a core module in the Platform's own process. A round trip over a Unix
socket per JSON fetch is not a cost worth trading the deny list for.

Enforcement therefore holds on Linux — the primary deployment target, given
[supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md)'s Docker Compose framing — and
degrades to convention on native macOS and Windows, where there is no equivalent
control worth the complexity. **Convention is still strictly better than what is
built**, where there is neither a path nor any observation of what a module
reaches.

Three honest limits:

- **HTTP(S) only.** A module needing non-HTTP egress — a DHT, a raw socket
  protocol — has no path through the proxy and is blocked outright wherever layer
  3 is active. No such module exists today, but this closes a door, and it should
  be reopened deliberately rather than by exception.
- **The deny list is only as good as its contents**, and an operator override
  re-opens the LAN by design.
- **Layer 3 is unavailable on some platforms**, so the guarantee is
  deployment-dependent — which must be stated wherever it is described, not
  claimed uniformly.

### The Platform owns the process; the Supervisor owns the install

The Supervisor installs, verifies, pins versions and holds the selected set, then
hands the Platform a list of installed modules and their binaries
([platform#40](0040-module-distribution-and-trust.md)). **The Platform spawns,
health-checks, restarts with backoff, and kills.**

Two reasons. A module crash must be a **degraded capability, not a Generation
event** — routing restarts through the Supervisor would make a wedged third-party
process a host-lifecycle concern, which is the coupling the tier split exists to
break. And the Platform is the only component that knows whether a module is
*answering* as opposed to merely running, so detection and remedy belong together.
This keeps the Supervisor small ([supervisor#1](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0001-supervisor-as-host-manager.md))
and puts runtime supervision in the runtime
([platform#3](0003-platform-as-execution-kernel.md)).

### A module owns private state, and only private state

[platform#2](0002-module-storage-and-delivery-model.md) §2 and
[platform#8](0008-capabilities-do-not-own-stores.md) are unchanged: **content lives
in the Platform's object graph, in the Platform's single consistency domain, and
nowhere else.**

What a separate process makes possible is a module-local cache, cursor,
rate-limit ledger or dialect table
([module-stremio-addons#2](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0002-modules-as-anti-corruption-layers.md)) in the module's own file —
its own SQLite, its own anything, because its dependencies are its own. The line
is that such a store must be **destroyable without data loss**: it is not backed
up as Platform data, no part of a user's library lives there, and deleting it
costs at worst a re-fetch. A module that needs durable content storage is a
Platform evolution request, exactly as [platform#2](0002-module-storage-and-delivery-model.md) says.

### Required capability classes can now go absent at runtime

[platform#23](0023-metadata-as-required-capability.md) makes metadata and search
required and absence fatal before the serve loop. Out of process, a required role
can become unavailable while running, which that record could not have
contemplated.

**Composition-time absence stays fatal**
([platform#38](0038-platform-binary-built-by-ci.md) holds that check). **Runtime
absence is a degraded state** — reported to the Supervisor, reflected in
capability-gated affordances ([platform#24](0024-capability-gated-affordances.md)),
and never a process exit. Killing the Platform because a third-party process
crashed would forfeit the containment that moving it out of process bought.

## Alternatives considered

**Let the SDK take the gRPC dependency.** *Rejected.* One import instead of two is
a real convenience, and it is what most SDKs do. But it forces every module to
resolve grpc-go and protobuf at versions the Platform effectively pins, which is
the precise failure [sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md) named when
it refused OpenTelemetry, and it publishes an implementation choice as part of a
contract third parties compile against.

**A hand-rolled stdlib-only protocol, so the harness is dependency-free too.**
*Rejected.* It would let `sdk` ship the harness with no split. But framing,
streaming, cancellation, deadlines and bidirectional flow control are exactly the
things that are tedious to get right and painful to get wrong, and owning them
buys only the avoidance of a nested module.

**Generate the Go interfaces from the proto, making the wire the contract.**
*Rejected.* It removes the dual-source-of-truth obligation, which is the real cost
of this decision. But generated stubs are not the hand-written, documented
interfaces two modules are already built against, `Caller` could not stay opaque,
and the contract would become un-implementable except over RPC — which would
break the core tier.

**Go plugins (`plugin`).** *Rejected.* Exact toolchain and dependency-version
match between host and plugin is strictly worse skew than the build pipeline this
replaces, it is effectively Linux-only, and it still shares the address space —
paying dynamic loading's compatibility cost for none of the isolation.

**WASM.** *Rejected for now, worth revisiting.* It would give real sandboxing
rather than process-level containment, and one portable artefact instead of a
build matrix. Against it: modules make outbound HTTP calls to arbitrary services,
which is awkward through WASI; Go-to-WASM adds toolchain constraints; and it
narrows what a module can be for benefits that are mostly not the ones driving
this thread. If a security sandbox becomes the priority, this is where to look.

**A signed short-TTL bearer token instead of a handle.** *Rejected*, as argued
above: a TTL is a retention window, and retention is the risk.

**Direct egress with an instrumented client and no proxy.** *Rejected, and this
reverses an earlier draft of this record.* It is simpler, it preserves trace
continuity, and it argues that since an in-process module can already ignore the
client it was handed, nothing is really lost. That argument mistakes the
in-process situation for a floor rather than a hole: a module reaching PostgreSQL
directly is possible today and would remain possible, when the process boundary is
the first thing that could prevent it. The proxy costs a socket round trip on
small JSON fetches, which is not worth trading for.

**Terminate TLS at the proxy to inspect content.** *Rejected.* It would give full
visibility into what a module sends and receives, and it means Mosaic
man-in-the-middles a third party's TLS as a matter of routine. Host-level
attribution answers the question that is actually being asked.

**A static host allowlist declared in the manifest.** *Deferred, not rejected.* It
is the right end state and it cannot be built yet: a Stremio addon's hosts are
whatever URLs the user pasted, and the Platform stores module settings
uninterpreted by design ([platform#17](0017-module-settings.md)), so it cannot derive
the list. Recording what a module contacted is achievable now; allowlisting what
it *may* contact lands with module-granular permissions
([platform#13](0013-how-a-capability-acts.md)), and the proxy is where it will
attach.

**One container per module, with no network.** *Named as a deployment option, not
the model.* It is the strongest and most portable form of layer 3 — the container
runtime denies the network, a shared volume carries the socket, and it works
identically wherever Docker does. It is rejected as *the* model because it
requires the Platform to drive a container runtime, which breaks the native-binary
install and turns the Platform into an orchestrator. An operator who wants it can
have it without the architecture depending on it.

**The Supervisor owns module processes.** *A genuine alternative.* It keeps all
process management in the durable layer and gives one place to look when something
will not start. Rejected on the degraded-capability-not-Generation-event argument.

## Consequences

- **A module author's code does not change**, beyond a `main.go`. This is the
  property the whole design is arranged around.
- **[platform#16](0016-optional-module-composition.md)'s named debt is paid.** The
  Platform's `go.mod` requires `module-stremio-addons` directly, which
  [sdk#1](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0001-sdk-as-public-contract-language.md) forbids and [platform#16](0016-optional-module-composition.md)
  recorded as a bridge. An extension module is not a Go dependency of the Platform
  at all.
- **Egress ends up stronger than what is built, and the strength comes from the
  operating system rather than from the protocol.** That is worth noticing,
  because it is the general shape of what the boundary is for: the process is not
  itself a control, it is the thing controls can finally be attached to.
- **Seam 9 gets better, not merely preserved.** In process, the Platform sees
  outbound calls only when a module uses the client it was handed. Through the
  proxy it sees them whether the module cooperates or not, attributed per module.
- **The process boundary is still not a security sandbox in general.** Egress is
  one axis and it is now controllable; the filesystem, the privileges of the uid
  the process runs as, and what the module does with the authority it is handed
  are not. `docs/index.md`'s statement that installing a community module means
  running arbitrary code with Platform authority stays true. What has changed is
  that it is no longer *uncontainable in principle* — the boundary is where a
  filesystem policy or a permission model would attach next, and egress is the
  first instance of that rather than the whole of it.
- **The Go interfaces and the `.proto` are two sources of truth that must agree.**
  Nothing enforces it but codegen discipline and tests. This is the recurring cost
  of the central decision, and it is worth naming as ongoing rather than one-off.
- **Module-granular permissions become enforceable for the first time.** A
  boundary that all module calls cross is a boundary at which a declaration could
  be checked. [platform#13](0013-how-a-capability-acts.md)'s open question is
  strengthened, not answered.
- **A module can now be observed as a process** — memory, CPU, restarts — which
  the ambient telemetry design ([platform#31](0031-telemetry-is-ambient-in-context.md))
  has no vocabulary for yet.
- **`ImportRequest` grows a wire representation.** Its `Settings []byte`
  ([platform#17](0017-module-settings.md)) crosses unchanged, which is a small
  vindication of storing module settings as opaque JSON.

**Open, deliberately.**

- **Callback chattiness.** A tree import makes many `ContentService` calls, each
  now a round trip. A Unix socket is fast enough that this is probably fine, but
  it should be **measured against a real Stremio import before the protocol is
  fixed**, and allowed to send the service shape back for coarser, batched verbs.
- **Crash-loop policy** — backoff ceiling, auto-disable, how an admin is told.
- **Resource limits** — whether the Platform enforces memory and CPU per module or
  only observes them.
- **Which layer-3 mechanism**, per platform. Network namespace, dedicated uid plus
  a firewall owner rule, and seccomp on `connect(2)` all deliver the property; the
  choice depends on what privileges the Supervisor can rely on having in a
  container, and should be settled against a real deployment rather than on paper.
- **Filesystem policy.** Egress is contained; what a module process may read and
  write is not, and it is the obvious next axis now that there is a boundary to
  attach one to.

## Implementation implications

Build order, smallest first:

1. **`sdk/host` and the Platform's extension host**, proven against a trivial
   in-repo module implementing one role. Nothing user-visible; establishes the
   wire, the handshake and the handle.
2. **Move `module-stremio-addons` out of process.** The real test: it implements
   four roles, makes many `ContentService` callbacks per import, and emits
   telemetry ([sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)), so it
   exercises every direction of the boundary at once. This is where round-trip
   cost and the handle design get measured rather than assumed.
3. **Process lifecycle** — spawn, health, restart, backoff — in the Platform.

Step 2 is the one that can invalidate step 1's protocol, and should be scheduled
as though it will.
