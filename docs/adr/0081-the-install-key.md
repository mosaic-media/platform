# The install key

**Status:** Proposed. The sealing envelope is built (`internal/adapters/crypto/sealer.go`); the key it needs is not, which is what this record is for. Nothing generates, stores or reads an install key today.
**Date:** 2026-08-10

Arises from [platform#79](0079-totp-is-the-second-factor-that-works-everywhere.md),
which needs a TOTP secret encrypted at rest, and answers a question that record
did not ask. It supersedes nothing.

## Context

[platform#79](0079-totp-is-the-second-factor-that-works-everywhere.md) acknowledged that "a database breach exposes the factor" and decided no
mitigation. Encrypting the secret at rest is the mitigation, and taking it
turned up a fact that was not visible from any document:

**There is no durable encryption key anywhere in the Platform.** Every key that
exists is deliberately process-scoped and regenerated on every boot — the
artwork URL HMAC, the playback ticket key, and the telemetry pseudonymisation
salt. That is correct for all three: each protects something that does not
outlive the process.

The mechanism that looks like custody does not run. `internal/platform/secrets`
has a Broker, an OS-keychain store and an encrypted local vault, and **it has no
production caller at all** — nothing constructs it, and nothing supplies the
recovery key its vault requires. Its purpose is narrower than its name suggests:
it exists for `secret://` indirection in *operator configuration*, and the only
field that uses it is the PostgreSQL password.

So a secret that must survive a restart has nowhere to live. Two things are
already waiting on exactly that and say so in their own comments: the telemetry
salt, whose home is described as "the Secret Broker … not built"
([platform#34](0034-redaction-classes-are-the-pii-boundary.md)), and this.

**The threat being addressed is specific, and naming it decides the design.** It
is not an attacker with the running process — against that, a key the process
can read is no defence and none is claimed. It is the way this data actually
escapes a self-hosted box: a database backup, a replica, a dump pasted into a
bug report, a disk sold on. **All of those carry the database and none of them
carry the filesystem beside it.**

## Decision

**One install key, generated on first run, stored in a file beside the instance
identity and never in the database. Losing it costs every enrolled user their
second factor, and that is the accepted failure mode.**

- **A file, not the database.** The whole value of the key is that it is not in
  the thing being protected. A key stored in PostgreSQL beside the ciphertext
  protects against nothing, and is the mistake this is easiest to make.
- **Generated on first run, `0600`, written atomically.**
  `internal/adapters/instance/file.go` is the precedent and the shape to follow:
  one small file outside PostgreSQL, temp-file plus fsync plus rename, so a
  power cut cannot leave a half-written one.
- **Not the OS keychain, and not the vault.** The keychain is unavailable on a
  headless server, which is where Mosaic runs; the vault needs a recovery key
  that has no home either, which is the same problem one level down.
- **The key is the install's, not a user's or an operator's.** No deployment has
  to supply it, because a self-hosted owner will not, and a mechanism that is
  off by default for most installs protects nobody.
- **Losing it is survivable and the survival path already exists.** Every user
  with a second factor also has recovery codes ([platform#79](0079-totp-is-the-second-factor-that-works-everywhere.md)), which are hashed
  rather than encrypted and are therefore unaffected. A lost key means every
  enrolled user signs in with a recovery code and re-enrols. That is a bad day
  and not a lost account, and it is the trade taken deliberately against a
  secret sitting in plaintext in every backup.
- **A missing key on a install that has sealed data is a refusal, not a
  regeneration.** Generating a fresh one would silently make every sealed value
  unopenable while looking like a clean boot. It must say what happened.
- **The telemetry salt should move here too**, which is the second consumer that
  makes this a shared facility rather than a TOTP detail — but not in the same
  change, because persisting a salt that is currently per-process changes what
  the pseudonyms mean across a restart, and that is its own decision.

## Alternatives

**Store the TOTP secret in plaintext**, as much self-hosted software does.
*Rejected*, and this is the alternative the owner considered and declined: a
leaked backup hands an attacker permanent code-minting for every enrolled user,
and the factor's whole purpose is to survive a password compromise.

**Require the operator to supply a key** through an environment variable or
config field. *Rejected.* A self-hosted media server's owner will not set one,
so the feature would be unencrypted for almost everyone while appearing to be
encrypted for all — the worst of both. It also puts custody on the person least
equipped to hold it.

**Use the OS keychain, falling back to the encrypted vault** — the mechanism
`internal/platform/secrets` already describes. *Rejected on the facts.* The
keychain is unavailable on a headless Linux box, which is the normal deployment,
and the vault's own key has no source: the fallback path needs custody the
codebase does not have, so choosing it would mean building this decision anyway
with an extra layer on top.

**Derive the key from something the install already has** — the instance id, the
hostname, the database DSN. *Rejected.* All of them travel with a backup or are
guessable, so the ciphertext would be openable by whoever holds the thing it is
meant to be protected from.

**Do it properly with a key hierarchy and rotation**: a master key wrapping
per-record data keys, with re-wrap on rotation. *Rejected for now, and it is the
honest end state.* There is no rotation story anywhere in the codebase and no
precedent for re-encrypting a column, so building one here would be inventing a
migration pattern for a feature that has no users yet. The versioned envelope is
what keeps that road open — a sealed value says what it is, so a later scheme can
tell its own values from the ones it replaced.

## Consequences

- **Mosaic acquires a file whose loss has a real cost**, and it has to be said
  where a person will read it: back this up, and separately from the database or
  the exercise is pointless.
- **A backup that includes the key file protects nothing.** The instruction is
  the inverse of the usual one and will be got wrong, which is an argument for
  the eventual backup slice (M5) treating the two as separate artefacts rather
  than one archive.
- **The first durable key in the repository** — everything before it was
  process-scoped by design, so this is a genuinely new kind of thing to own,
  with a new failure mode at boot.
- **It does not defend against a compromised host**, and the record says so
  rather than letting the feature read as more than it is.
- **The telemetry salt gains a possible home**, closing something [platform#34](0034-redaction-classes-are-the-pii-boundary.md) left
  open — separately, and not by this change.
