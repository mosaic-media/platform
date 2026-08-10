# Two signing keys, held offline, rotated by overlap

**Status:** Proposed. Partly built: one of the two keys exists and is in use —
the registry key signs the live index and the Platform verifies it. The release
key does not exist, nothing verifies a release artefact, and the custody and
rotation procedures below are written down here and nowhere else yet.
**Date:** 2026-08-09

Settles the "key custody Mosaic must operate" that
[platform#40](0040-module-distribution-and-trust.md) named as ongoing work and left
undefined, and is what [platform#38](0038-platform-binary-built-by-ci.md)'s unbuilt
signing step waits on. Does not change what is signed or how a signature is
checked — [platform#40](0040-module-distribution-and-trust.md) decided that and it
stands.

## Context

**There is exactly one signature in Mosaic today, and one key makes it.**
`registry`'s CI runs `modulesign sign-index` over the aggregated catalogue with
`REGISTRY_SIGNING_KEY`; the Platform embeds the public half as
`internal/adapters/extension/mosaic-official.pub` and verifies the index against
it before installing anything.

Two things about that chain are easy to get wrong from the outside, and both
matter here.

**A module signs nothing.** Its release runs `modulesign build-manifest`, which
emits an *unsigned* `manifest.json` carrying its binaries' digests and URLs. The
registry downloads those manifests, aggregates them and signs the result. So a
module binary's integrity chains to the index signature, not to a signature of
its own, and there is no per-module key to hold. (`registry`'s
`scripts/assemble.sh` calls it "a signed manifest" in an error message, which is
the one place this reads as though modules sign.)

**The Supervisor verifies nothing, because it downloads nothing yet.** The key
above is embedded in the *Platform* and covers what the Platform installs:
extension modules. Release artefacts — the Platform binary, the Shell binary —
are downloaded by the **Supervisor**, which has no embedded key and no
verification code. So "we already have a signing key" is true and does not reach
this problem.

The live question is therefore not whether to sign but **how many keys, held
where, and what happens when one has to change** — and one property of the
existing code decides more of it than it looks: `Keyring.verify` tries every
trusted key rather than requiring a signature to name one, so a keyring can
trust two keys at once. That is the whole mechanism rotation needs, already
built and never exercised.

## Decision

### Two keys, no root

| Key | Signs | Verified by | Cadence |
|---|---|---|---|
| **`mosaic-official`** (exists) | the registry index, and through it every module manifest and binary digest | the **Platform**, embedded public half | every module release |
| **`mosaic-release`** (new) | the Platform binary, the Shell binary and their checksums | the **Supervisor**, embedded public half | every Mosaic release |

**They are separate because their blast radii are different and their exposure
is inverted.** The registry key is used by CI on every module release — the most
frequently exercised secret Mosaic has — and a compromise of it serves a
malicious module: real, and bounded by the extension boundary
([platform#39](0039-extension-module-boundary.md)) to a separate process with
controlled egress. A compromise of the release key serves a malicious *Platform*,
which is bounded by nothing. Signing the second with the key that does the first
would hand the total outcome to the higher-exposure secret for no saving beyond
one `genkey`.

**No root key, and that is a deliberate stop.** A root that signs delegations to
these two is the more robust arrangement and is what TUF exists for; it would let
a key be replaced without shipping a binary. It also introduces an offline
ceremony, a delegation format, an expiry model and a second thing to get right in
a project whose whole install story is "download a binary". The two-key
arrangement below rotates through releases instead, which is slower and needs no
new machinery. If a third or fourth signing need appears, that is the moment to
revisit this and not before.

**`mosaic-official` keeps its name.** It is the keyring id, the repository name
(`OfficialRepositoryName`) and the embedded filename, and renaming it to
something like `mosaic-registry` for symmetry would be churn across three
repositories for a word.

### Custody: the CI secret is a copy, never the only copy

Both private keys are generated with `modulesign genkey` **off CI**, and each
lives in two places:

1. An offline backup the project owner holds — a password manager entry or
   equivalent, outside GitHub.
2. A GitHub Actions secret in the repository whose workflow signs with it:
   `REGISTRY_SIGNING_KEY` in `registry`, and the release key in `platform` and
   `web` (both link artefacts a Supervisor will verify).

**This is the actual content of the gap [platform#40](0040-module-distribution-and-trust.md) named**, and it is one sentence
because the failure it prevents is one sentence: a GitHub Actions secret cannot
be read back out. A key that exists only as a CI secret is a key nobody can back
up, audit, or move to a new repository — and losing it means every installed
Platform, carrying the old public half in its binary, can no longer verify a new
index.

The workflow that uses a key shreds its decoded copy after signing, as
`registry`'s already does.

### Rotation: overlap, then drop

`Keyring.verify` trying every trusted key is what makes this a procedure rather
than a flag day.

1. Generate the new key. Add its public half **beside** the old one in the
   verifier, so the keyring trusts both. Keep signing with the old.
2. Release the verifier — a Platform release for the registry key, a Supervisor
   release for the release key — and let installs pick it up.
3. Switch the signing workflow to the new key. Everything that took step 2 keeps
   verifying; anything that did not is still on the old key and still works.
4. In a later release, remove the old public half.

**The overlap window is bounded by how long an install can go without updating,
and that is not knowable**, so step 4 is a judgement rather than a schedule.
Removing the old key too early strands exactly the installs least able to
recover: the ones that have not updated.

**A compromised key does not get this procedure.** Overlap exists so a planned
rotation costs nobody an outage; a compromise wants the old key untrusted
immediately, which is the opposite. See below.

### Revocation stays open, and this record says why rather than inventing one

An installed Platform fetches one index from one URL and has no side channel. It
cannot be told a key is revoked by anything except a build that no longer trusts
it — so the only revocation available today is: release a binary with the key
removed, and rely on users updating. That is slow, it is exactly backwards from
what a compromise needs, and it does not reach an install that never updates.

[platform#40](0040-module-distribution-and-trust.md) listed revocation as an open consequence and it remains one. What this
record adds is the reason it is hard, so the next attempt starts from the
constraint rather than rediscovering it: **revocation needs something an install
can reach that is not the thing being revoked.** A signed expiry in the index,
so a stale catalogue stops being trusted on its own, is the cheapest candidate
and is not decided here.

## Alternatives considered

**One key for everything.** *Rejected*, and it is the status quo extended rather
than a change — which is what makes it tempting. It gives the most-exercised
secret in the project authority over the Platform binary itself.

**A root key signing delegations.** *Rejected for now*, with the reasoning above:
it is the right answer at a scale Mosaic is not at, and it buys rotation without
a binary release at the cost of an offline ceremony and a delegation format.
Named rather than dismissed, because two more signing needs would change the
arithmetic.

**A key per module.** *Rejected.* It is what the assemble.sh error message reads
as though Mosaic does, and it does not follow: a module publishes an unsigned
manifest whose digests are authenticated by the index. Per-module keys would mean
the registry vouching for a set of keys instead of a set of digests, which is
more moving parts for the same guarantee — and it would make enrolling a
third-party module a key-exchange rather than a catalogue edit.

**A hosted signing service (Sigstore, cosign with an OIDC identity).** *Rejected,
and worth naming because it is the modern default.* It removes the custody
problem entirely by removing the long-lived key. It also makes verification
depend on a transparency log and a root of trust Mosaic does not operate, in a
binary whose stated job is running when everything else is unavailable, on
installs that may be offline. The custody burden here is two keys held by one
person, which is proportionate.

## Consequences

- **`modulesign genkey` produces the release key; nothing else changes in the
  tool.** It already writes a raw public half beside the private one, which is
  the form both embeds take.
- **The Supervisor gains an embedded public key and the code to verify with it**,
  which lands with its download-and-activate work — verification is the step
  between downloading and executing, so it is not a separate slice.
- **`platform` and `web` release workflows gain a signing step**, over the
  binaries and the `SHA256SUMS` each already produces. Both currently say signing
  waits on key custody; this is the record that unblocks them.
- **The public halves are embedded, so a key change is a release.** That is the
  cost of having no root, paid on a cadence measured in key rotations rather than
  in releases.
- **A compromise of `mosaic-release` is unrecoverable without a user acting**,
  because the Supervisor verifying its own updates with the key that was
  compromised is a circle no arrangement here breaks. Stated rather than
  mitigated: a root key is what breaks it, and that is the trade this record
  makes.
- **`registry`'s "signed manifest" error message is inaccurate** and should say
  a manifest and binaries, since nothing signs a manifest. Small, and worth
  fixing where a third-party module author will read it.
