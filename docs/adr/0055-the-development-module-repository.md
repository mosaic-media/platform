# The development module repository, and the build tag that keeps it out of releases

**Status:** Accepted; **built.** The override, its build-tag guard and the dev
stack's local registry all land with this record, and the loop below was
exercised end to end — a working-copy edit to `module-stremio-addons` reached a
locally signed index and was installed through the Platform's own install action.
The guard is absence rather than an off switch: `devregistry_off.go` reads no
environment, and `docker-compose.test.yml` gates both configurations by running
`go vet -tags mosaicdev ./...` and the tagged tests after the ordinary suite.
**Date:** 2026-07-26

Extends [platform#40](0040-module-distribution-and-trust.md) (the signed repository
and its trusted key), [platform#49](0049-the-platform-manages-extension-modules.md)
(the Platform is the actor for the extension lifecycle) and
[platform#51](0051-extension-installation-is-user-initiated-and-persistent.md) (an
extension is installed by a user at runtime and re-adopted across restarts).
Touches [platform#50](0050-deployment-topologies.md)'s SSRF containment, because
reaching a local repository means reaching a private address. It changes nothing
about what a shipped Platform does.

## Context

[platform#51](0051-extension-installation-is-user-initiated-and-persistent.md) took the extension modules out of the Platform binary, and it was
right: an extension is installed at runtime, not compiled in, so
`module-stremio-addons`, `module-aiostreams` and `module-fanart-tv` are not
Platform dependencies and correctly appear in neither `go.mod` nor the dev
stack's Go workspace.

**What that left is a tier of the system with no development loop at all.** The
core modules are overlaid from their sibling checkouts and an edit reaches a
running Platform on a restart. For an extension module there is no equivalent,
because the only path into a Platform is the install path, and the install path
leads to exactly one place:

```go
const OfficialRepositoryURL = "https://mosaic-media.github.io/registry"
```

compiled in beside a `go:embed`ed public key, with no environment override —
the Platform reads twelve `MOSAIC_*` variables and none of them touches the
repository. So seeing a one-line change to an extension module actually *run*
meant: tag it, cut a release, cross-compile five targets, build and upload a
manifest, hand-edit the registry's `registry.yaml`, dispatch the registry's
publish workflow, wait for GitHub Pages, then install through the UI. Minutes at
best, and irreversible: a tag pushed to try something is a tag that exists
forever.

The cost of that is not only the wait. It is that the whole producer side —
manifest assembly, index signing, digest verification, the handshake — was
exercised **only** by CI against real releases, so the fastest way to learn that
a change to it was wrong was to publish a wrong one.

The obvious fix is an environment variable, and the obvious fix is where the
danger is. The repository's key vouches for every binary the Platform downloads,
spawns as a subprocess, and hands its own authority to. Anything that can name a
different URL and a different key can have a Platform run arbitrary code as
itself. Adding a variable that does that puts a remote-code-execution path into
every release, one environment edit away, on surfaces where editing
configuration is emphatically not supposed to mean executing code: a NAS app's
env table, a hosting panel, a compose file copied from a forum post. [platform#50](0050-deployment-topologies.md)
named those topologies; each of them has someone other than the developer
editing that environment.

## Decision

**A development build may point the official repository at a local one. A
shipped build contains no mechanism that could. The two are separated by a build
tag, not by a runtime check — and signature verification is re-keyed, never
bypassed.**

### The override

A binary built with `-tags mosaicdev` reads two variables:

- `MOSAIC_DEV_REPOSITORY_URL` — the base URL an index is fetched from
- `MOSAIC_DEV_REPOSITORY_KEY` — a **path** to the ed25519 public key that index
  is verified against

Both or neither. Half of it is a boot failure rather than a fallback: a URL with
no key would have to fall back to the official key, which is a repository nobody
can sign for, and a key with no URL would trust a second key for Mosaic's own
index — both strictly worse than either the default or the intended override. A
key file that is not a well-formed ed25519 public key is likewise a boot
failure, the same check the embedded official key already gets and for the same
reason: the alternative is discovering it as "nothing verifies" at the first
install.

The key is named as a *file path*, never as bytes on the wire. A key fetched
from the repository it authenticates verifies everything that repository ever
serves, which is not verification. A path is something the operator of the stack
put there out of band — which is what compiling the official key in also is.

### Verification stays on, and is what makes the loop worth having

The local registry is a real repository: `modulesign` builds a manifest per
module from the module's own `--mosaic-manifest` identity plus the digest of the
binary just built, `build-index` aggregates them, `sign-index` signs the result
with a throwaway development key. The Platform then does **exactly** what it
does against the published registry — fetch the index and its detached
signature, verify the signature, treat the inline manifests as authenticated by
it, check the SDK major, download the binary, check it against the signed
digest, spawn it, check the handshake against the declared manifest.

This is the point rather than a nicety. An override that skipped verification
would exercise a path that does not exist in production and would prove nothing
about the one that does. What moves is *whose key* vouches for the catalogue and
*where* the catalogue is. A development key signs a development index.

### The guard is a build tag

`internal/adapters/extension/devregistry.go` (`//go:build mosaicdev`) holds the
whole mechanism — the environment read, the parser, and the fetcher below.
`devregistry_off.go` (`//go:build !mosaicdev`) is what an ordinary `go build`
compiles: two functions that read nothing and return no override. There is no
code path in a release binary from an environment value to a trusted key, which
is a stronger statement than any branch could make.

The alternative was a runtime check plus a loud warning. It was rejected because
no check makes the mechanism safe to ship: a flag, an acknowledgement variable
or a banner nobody reads all leave the path present, and the attacker who can
set two variables can set three. The question is not how loudly a release should
warn about repointing its module repository — it is whether a release should be
able to.

### The dial guard is relaxed with it, and that is the sharper half

A local registry is on a private address. `netguard` refuses exactly those, and
correctly: an install is the Platform fetching a URL on a user's behalf, which
is the SSRF shape the guard exists for ([platform#50](0050-deployment-topologies.md)). So the override also selects
an unguarded fetcher — and *only* under the tag, and only when an override is
actually configured.

This is the more dangerous of the two relaxations, and the better argument for
the tag. It is not merely "trust a different key", it is "reach the host's own
network". A shipped binary contains no code that can.

### It is conspicuous anyway

The overridden repository is marked **not** `Official` — it is demonstrably not
Mosaic's — and the composition root logs it at `Warn` on every boot, naming the
URL, the fingerprint of the key now vouching for every module the Platform will
run, the official URL it is *not* using, and the two variables that did it. The
tag is what makes the override impossible in a release; the warning is what
makes it impossible to be in one by accident.

### Both halves are tested, in the builds they exist in

`TestTheEnvironmentCannotRepointAShippedBuild` runs in the default build and
sets both variables with a perfectly valid key, asserting the compiled-in URL
and key come back unchanged. Its counterpart under the tag stands up a signed
index on loopback and installs from it — which takes the URL override, the key
override and the relaxed dialer all working — and a third refuses an index
signed by a key the Platform was not given. Neither test can run in the other's
build, so the gate runs both: `go vet -tags mosaicdev ./...` and the tagged
tests for the affected package, beside the ordinary suite.

## Alternatives considered

**A runtime environment override with a loud warning.** *Rejected*, as above:
it ships the mechanism. The warning is kept — as well as the tag, not instead of
it.

**A separate "trust this extra repository" surface, third-party style
([platform#40](0040-module-distribution-and-trust.md)).** *Rejected for this purpose*, though it remains the right shape for
its own: adding a third-party repository is a user's informed consent decision
with a UI to build, and using it as the development loop would mean either
building that surface first or seeding consent from configuration — which is the
same hole through a longer tunnel. A local development registry is not a user's
trust decision; it is a property of the build.

**Skip verification when a development flag is set.** *Rejected.* The verified
install path is the thing under test. A loop that bypasses it tests the bypass.

**Serve the local registry over HTTPS with a generated CA, keeping the dial
guard.** *Rejected.* The certificate protects nothing the index signature does
not, and the dial guard would still refuse the private address — so the
relaxation is unavoidable and dressing it up would only make it harder to see.

**Point the dev stack at the real registry and publish pre-release tags.**
*Rejected.* It makes every experiment a permanent public artefact and leaves the
loop as slow as it was.

## Consequences

- **The dev stack runs a binary that differs from the released one**, and this
  is the real cost. It is confined to which repository and key are trusted and
  which dialer fetches from them; every check that decides whether a module runs
  is the same code on both sides of the tag. The gate builds and vets both
  configurations so the difference cannot silently grow.
- **The producer side is now exercised on every local run.** `modulesign`'s
  manifest and index assembly, previously reached only by a module's release
  workflow, runs each time the local index is rebuilt.
- **The local registry catalogues all three extension modules**, built from the
  sibling checkouts for the container's own platform only. Its manifests carry
  one binary each, which the Platform accepts because it looks for its own
  platform and no other.
- **Local versions are unmistakable**: `local-` plus `git describe`, so a
  working-copy build reads `local-v0.28.0-1-gb34c5be-dirty` in the catalogue,
  in the consent overlay and in the install record. A local build cannot be
  mistaken for a release in the surface or in the store.
- **A rebuilt index does not reach an already-installed module**, and that is
  [platform#51](0051-extension-installation-is-user-initiated-and-persistent.md) working rather than a gap: boot re-adopts the *pinned* bytes from
  disk instead of silently upgrading to whatever a catalogue now lists. The
  local loop is therefore rebuild-then-reinstall, which is documented rather
  than smoothed over — smoothing it would mean weakening the pin.
- **Installed extensions move to a volume in the dev stack.** The default
  install directory is relative to the working directory, which under the
  overlay is the bind-mounted checkout, so every install would have written a
  Linux binary into the host repository. Unrelated to trust, found by making
  installs actually happen.
- **Open:** whether the *published* registry gains a staging channel. This
  record deliberately does not answer it — a local loop is the cheap half of
  that problem, and the expensive half (a second signing key, a second index, a
  channel selector a user could be talked into changing) is a different decision
  with a different threat model.
