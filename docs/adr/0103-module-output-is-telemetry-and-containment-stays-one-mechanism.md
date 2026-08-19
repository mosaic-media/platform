# A module's own output is telemetry, and containment stays one mechanism

**Status:** Accepted. **Not built.** Answers both questions
[sdk#7](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0007-go-plugin-as-the-extension-harness.md)
left open, recorded here because both mechanisms are the Platform's: the harness
adaptation lives in `internal/adapters/extension`, and containment is
[platform#93](0093-filesystem-containment-is-applied-where-the-os-allows.md)'s.

## Context

[sdk#7](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0007-go-plugin-as-the-extension-harness.md) adopted go-plugin and left two things open, neither of which the SDK can
settle — the SDK carries no implementation, and both of these are implementations.

**Whether go-plugin's log forwarding feeds the telemetry surface.** A module
process's stderr is where a panic and a failed start appear. It is also the one
thing [platform#31](0031-telemetry-is-ambient-in-context.md)'s ambient design has
no vocabulary for, because everything else arrives as structured fields from code
that chose to emit them, and this arrives as whatever the runtime printed.

**Whether `plugincontainer` becomes a supported deployment**, with documentation
and tests, or stays a possibility an operator assembles.

## Decision

**A module's forwarded output is adapted into the telemetry surface, carrying the
module's identity.** It is the only place a crash is visible, and two observability
streams reliably means the one with the crash in it is the one nobody wired to a
screen. A module that dies at startup should be diagnosable from the same place as
everything else about that module.

It arrives as a message with the module's identity attached rather than as
structured fields, and that is a real difference from every record beside it —
worth marking on the record itself rather than letting it look like an ordinary
emission that happens to be badly written. **Nothing a module prints may be
trusted as classified**: it is text from another process, so it is treated as an
unclassified payload under
[platform#34](0034-redaction-classes-are-the-pii-boundary.md) rather than being
assumed safe because it arrived through the telemetry path. A module that prints
its own credential must not have it silently promoted to a structured field.

**`plugincontainer` is not a supported deployment.** platform#93 settled
containment: Landlock where the operating system allows it, honestly reported
where it does not. The value of that record is that there is exactly one answer
and it does not overstate itself. A container path would be a second containment
story with its own guarantees to keep true, its own posture to report, and its own
matrix in the gate — and the honest posture reporting that makes platform#93 worth
having would then have two answers to give.

It stays what [sdk#7](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0007-go-plugin-as-the-extension-harness.md) called it: an operator option, not the model.

## Alternatives considered

**A separate stream for module output.** *Rejected:* it keeps the structured
surface clean and puts the crashes somewhere only a person who already knows will
look.

**Discard module output entirely.** *Rejected:* go-plugin forwards it, so the
choice is where it goes, not whether it exists — and discarding it means a panic
leaves no trace at all.

**Parse module output into structured fields.** *Rejected:* it would be guessing
at another process's format, and the guess would be confidently wrong on exactly
the unusual output that matters.

**Support `plugincontainer` for the platforms Landlock does not cover.**
*Rejected, and it is the strongest alternative* — macOS and Windows get a reported
posture and no mechanism, and this would give them one. It is rejected on the cost
of a second containment story rather than on merit, and it is the thing to revisit
if the reported posture turns out to be what stops people trusting extensions.

## Consequences

**A noisy module can flood the telemetry sink.** The sink already bounds itself,
discards oldest and counts the loss, so the failure mode is defined — but a module
printing per request will now evict Platform records, which is a new way for one
module to degrade something shared.

**macOS and Windows keep a reported posture and no enforcement**, which is
platform#93's position and is now also the answer to "could a container fix that".

**[sdk#7](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0007-go-plugin-as-the-extension-harness.md)'s open list is empty**, which its Status line should say.
