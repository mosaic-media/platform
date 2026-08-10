# Core modules keep their repositories; CI carries the version bump

**Status:** Proposed
**Date:** 2026-07-24

Answers a question [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md) left implicit and
[platform#38](0038-platform-binary-built-by-ci.md) assumed. Supersedes neither.
Nothing here is built.

## Context

[architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md) puts core modules in the Platform binary and
records as a consequence that `module-remote-playback` "keeps its direct
`require` in the Platform's `go.mod`".
[platform#38](0038-platform-binary-built-by-ci.md) has Mosaic's CI build that binary
once per release. **Neither says how a core module's own release reaches the
binary**, and the answer today is: a person does it.

That friction is real and it is already documented as tribal knowledge — tag the
module, wait for or warm the Go module proxy, bump the `require` in the
Platform's `go.mod`, run the gate, commit. Five modules sit in that loop right
now, required at tags in `platform/go.mod`, so **every release of any of them is
also an edit to the Platform.**

The obvious fix is to move them into the Platform repository, where
`internal/modules/postgres` already lives. That was proposed, and it costs three
things worth more than the friction it removes.

**Licensing.** `module-tmdb`, `module-cinemeta`, `module-remote-playback` and the
rest are MIT. The Platform is AGPL-3.0-only with a linking exception. In-repo
means relicensing them or running a mixed-license repository, and
[architecture#1](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0001-licensing.md) governs that choice deliberately rather than
incidentally.

**The SDK-only boundary stops being compiler-enforced.** A separate Go module
*physically cannot* import `platform/internal/`. In-repo, that guarantee degrades
to a boundary test. [platform#12](0012-published-contract-surface.md)'s stop point —
if a capability needs a private Platform import, the contracts are not ready to
publish — is enforced by Go itself today, and a test is a weaker thing than the
toolchain refusing to compile.

**[architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md)'s tier mobility.** That record states that
"a module does not know which tier it is in, and moving one between tiers is a
build change rather than a rewrite." If core modules live in the Platform
repository, promoting one to the extension tier becomes a repository extraction
plus a relicense.

## Decision

**Core modules keep their own repositories and their own licenses. A core module
tag triggers an automated version bump in the Platform, opened as a pull request
and merged when the gate is green.**

### The bump is automated, not eliminated

Honest first: **`platform/go.mod` still changes on every core module release.**
Go modules offer no way around that, and any claim that this decision removes the
Platform edit would be false. What changes is that a bot writes the edit and runs
the gate rather than a person. The friction being removed is the manual sequence,
not the dependency.

On a module tag:

1. The module's release workflow dispatches to `platform`.
2. The Platform workflow **warms the Go module proxy first, with a retry**,
   because a freshly pushed tag is not immediately resolvable and this is the
   step that most often fails when it is done by hand.
3. `go get <module>@<tag>` and `go mod tidy`.
4. The full container gate — license headers, gofmt, vet, build, tests.
5. A pull request, auto-merged on green.

### A pull request, not a direct commit to main

The core set is first-party and curated, so a bot committing straight to `main`
would be defensible. Two reasons it is not chosen.

[platform#38](0038-platform-binary-built-by-ci.md)'s guarantee that **every released
binary is pre-proven** is a property of the gate having actually run. A required
check on a pull request is where that is enforced mechanically, rather than by
trusting the ordering of steps in a workflow.

And it **leaves a record of which module tag caused which Platform commit.** The
first time a core module bump breaks the build, that link is most of the
diagnosis. A bot commit landing on `main` with a generated message does not carry
it.

### A core module tag produces a green `main`, not a Platform release

Otherwise every module patch cuts a Platform release. Releases keep the cadence
[platform#38](0038-platform-binary-built-by-ci.md) gives them and pick up whatever
core module versions are on `main` when they are cut.

### Extension modules take the same trigger and a different artefact

Worth stating for the symmetry: a tag fires a workflow there too, but the
artefact is a **signed binary plus a manifest pushed to a repository index**
([platform#40](0040-module-distribution-and-trust.md)), not a `go.mod` bump. The
Platform is not a Go dependency of an extension module and never sees its version
at build time — which is exactly the debt
[platform#16](0016-optional-module-composition.md) recorded and
[platform#39](0039-extension-module-boundary.md) pays.

## Alternatives considered

**Move core modules into the Platform repository.** *Rejected* on the three costs
in Context; the licensing one alone is sufficient. Its merit is real and should
be recorded: one commit ships a core change, there is one repository to reason
about, and the Go module proxy leaves the loop entirely. **If the tag-dispatch
machinery proves unreliable in practice, this is the fallback**, and all three
costs are payable rather than prohibitive.

**Keep the manual bump.** *Rejected.* It works and it is what happens today. It
is also why the proxy-warming step exists as something a person has to remember,
which is the definition of a step that should be automated.

**A bot commit direct to `main`.** *Rejected*, as argued above — it forfeits both
the mechanical gate and the provenance link.

**A Go workspace or permanent `replace` directives.** *Rejected.* Every Mosaic
repository holds the rule that a `replace` must never land in a commit, and this
would make the Platform's build depend on sibling checkouts existing.

**Vendor core modules into the Platform.** *Rejected.* It keeps the separate
repositories and the licenses and removes the proxy from the build, but it
replaces a one-line version bump with a large diff on every release, and the
vendored tree is a second copy that can drift from the tag it claims to be.

## Consequences

- **MIT stays MIT.** No relicensing, and [architecture#1](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0001-licensing.md) is not
  reopened.
- **Go itself keeps enforcing the SDK-only boundary for core modules**, which is
  a stronger guarantee than the boundary test the in-repo alternative would have
  left, and it keeps [platform#12](0012-published-contract-surface.md)'s stop point
  executable.
- **[architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md)'s tier mobility survives intact.**
  Moving `module-fanart-tv` between tiers stays a build change.
- **CI becomes load-bearing for the core tier.** A broken dispatch means a core
  module release silently does not reach the binary — a failure with no red build
  anywhere, which is the class of failure this project has been bitten by before.
  It must be visible when it fails.
- **The Go module proxy stays in the release path**, which is a dependency on
  infrastructure Mosaic does not run. The warm-up retry mitigates it and does not
  remove it.
- **This is buildable today.** Almost everything else in the
  [architecture#3](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0003-two-module-tiers.md)–[platform#40](0040-module-distribution-and-trust.md)
  thread waits on a Supervisor that does not exist; this part waits on nothing.

**Open.** Whether the same dispatch should bump the **SDK** across every module
repository when the SDK tags. It is the identical problem one level down, it is
entirely manual today, and it is worse — an SDK tag currently requires seven
repositories to be updated by hand.
