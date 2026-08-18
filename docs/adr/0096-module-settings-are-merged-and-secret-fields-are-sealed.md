# Module settings are written by merge, and declared secret fields are sealed

**Status:** Accepted. **Not built.** Makes concrete the declarative surface
[sdk#10](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0010-the-sdk-carries-no-implementation.md)
described. **Breaking:** replaces `ConfigureModule`'s whole-document write, so
the SDK, the Platform and every module release together.

## Context

`ConfigureModule` takes a settings document and replaces the stored one.
`module-aiostreams` states the consequence in its own source: "every mutating
control is an Invoke of the Platform's configureModule command carrying the
complete new settings document".

So a toggle's action payload contains the module's API key. It travels to the
client, sits in its memory, returns on submit, and appears in anything that
observes an action —
[platform#34](0034-redaction-classes-are-the-pii-boundary.md)'s redaction classes
cannot see it, because they classify fields the Platform constructs and this is
an opaque blob passing through.

**The opacity that causes the leak is deliberate.**
[platform#17](0017-module-settings.md) makes a settings document the module's to
interpret and the Platform's to store uninterpreted. The Platform cannot redact
what it is not allowed to read. That is a genuine tension rather than an
oversight, and it is why the fix has to change the shape of a write rather than
add a filter.

The direction is not open.
[sdk#10](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0010-the-sdk-carries-no-implementation.md)
already decided that a module reaches sealing "through a *declarative* surface —
a settings field marked secret, sealed by the Platform — and never through
`Seal`/`Open`", because a `Seal`/`Open` primitive in a published contract names
an implementation and hands a module an encryption oracle. What was open is what
to build against that.

## Decision

**A settings write is a JSON Merge Patch ([RFC 7386]) against the stored
document, and that is the only semantic.** A control sends the field it changes
and nothing else, so a credential never enters an action payload. Merge Patch
reads structure without interpreting meaning, so
[platform#17](0017-module-settings.md)'s division survives: the Platform learns
that a document has fields, never what they mean.

[RFC 7386]: https://www.rfc-editor.org/rfc/rfc7386

**A null value deletes a field, per the RFC, and that is a trap worth naming.** A
module whose setting legitimately holds null cannot express it, and the SDK must
say so where an author will read it rather than leaving it to be discovered.

**A module declares which settings fields are secret.** The Platform seals a
declared field at rest and hands the module the plain value at invocation. The
module never seals, never unseals, never holds a key, and never learns which
mechanism was used — which is exactly the surface
[sdk#10](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0010-the-sdk-carries-no-implementation.md)
asked for.

**A declared secret is withheld from a settings screen.** The module renders
whether a value is set, and at most a mask. Three modules already do this by
convention with a `maskKey` helper; declaring the field makes it structural
rather than a habit each author has to keep.

**Merge is what makes withholding safe, and this is the load-bearing
interaction.** Withholding a secret from the screen means the screen cannot send
it back. Under a whole-document write that would *delete* it — the first toggle a
user touched would wipe their API key. Under merge, an absent field means
unchanged. Neither half works alone: merge without declared secrets closes the
transit leak and leaves the value plain at rest, and declared secrets without
merge is broken outright.

**The whole-document write is removed rather than deprecated.** Keeping it as an
opt-out would leave the leak open in every module that had not adopted merge,
while the roadmap said the leak was closed — a claim nothing would contradict.
The SDK, the Platform and all six modules release together.

**An existing stored value is sealed when it is next written.** A field cannot be
sealed retroactively, because until the module declares it secret the Platform
does not know which bytes it is. This means an install carries plaintext
credentials until each module updates and each setting is next touched, and that
window should be stated to operators rather than assumed away.

## Alternatives considered

**A merge semantic alone.** One change, no new concept, and it closes the leak in
transit. Rejected as insufficient now rather than wrong:
[supervisor#13](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0013-the-supervisor-takes-the-backup.md)
makes a backup a routine artefact that leaves the machine, so plaintext
credentials at rest travel with every copy.

**Declared secret fields alone.** The direct reading of
[sdk#10](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0010-the-sdk-carries-no-implementation.md),
with no change to how a write works. **Rejected because it does not function.** A
withheld field cannot be returned, and a whole-document write then deletes it.

**A bespoke merge format naming the fields a control touches.** Explicit, with no
sentinel semantics. Rejected: it is a format to specify, implement and keep in
step across the SDK and every client, where a specified standard already exists.

**JSON Patch ([RFC 6902]) rather than Merge Patch.** More expressive, and it can
express a null value without deleting it. Rejected as more than this needs: a
settings control sets a field, and an operation array is a general-purpose
document editor pointed at a configuration screen.

[RFC 6902]: https://www.rfc-editor.org/rfc/rfc6902

**Opt-in merge, with the whole-document write retained.** Nothing breaks and
adoption is gradual. Rejected on the reporting problem: the leak would remain
open wherever it had not been adopted, and nothing would say where.

## Consequences

- **The Platform's view of a settings document is no longer entirely opaque.** It
  reads structure to merge, and knows which fields a module declared secret. That
  is a real narrowing of [platform#17](0017-module-settings.md), narrower than
  reading meaning and wider than storing bytes.
- **A coordinated release across the SDK, the Platform and six module
  repositories**, three of which are compiled into the Platform binary and
  therefore move with its `go.mod`.
- **Null cannot be a settings value.** A module needing one must encode it some
  other way, and nothing will catch the mistake except the value disappearing.
- **Plaintext credentials persist until each setting is next written.** The
  migration is lazy by necessity, so an operator who never touches a setting
  keeps an unsealed one indefinitely.
- **[supervisor#13](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0013-the-supervisor-takes-the-backup.md)'s
  backups improve as a side effect**, carrying sealed rather than plain
  third-party credentials — but only for fields a module declared and a user has
  since written.
