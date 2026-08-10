# Redaction classes are the PII boundary

**Status:** Accepted (built, except its enforcement). Redaction happens at
construction and `Identifier` carries a salted hash, so a sensitive value is
never buffered. **The vet-style check below is unbuilt**, which leaves the rule
as developer discipline — the arrangement
[platform#41](0041-authorization-is-carried-in-the-type.md) demonstrated does not
hold. The `licenseheader` tool remains the model to copy.
**Date:** 2026-07-22

## Context

The conventional way to keep personal data out of telemetry is a scrubbing
pipeline: emit whatever, then match the output against patterns for emails, card
numbers, tokens and IP addresses, and mask what matches. It is popular because it
requires nothing of the developer.

It is also guesswork in both directions. It cannot recognise a username that
looks like a word, a media title that reveals something about a household, or an
API key that happens not to match the token pattern. And it runs *after* the
value has been formatted into a string, so by then the sensitive data has already
been assembled, buffered and possibly written — the scrubber is cleaning up
downstream of the leak, not preventing it.

Mosaic already has the stronger mechanism and it predates this work.
`diagnostics.Field` **fails closed**:

> Its `Value` is written verbatim only when `Redaction` is `domain.RedactionNone`;
> any other class — **including the zero value** — is replaced with
> `[REDACTED]` before the entry is ever serialized.

A field a caller forgot to classify is redacted rather than leaked. `String()`,
`Sensitive()` and `Secret()` make the classification a decision at the call site,
where the person writing the code knows what the value is, rather than a
pattern-match later where nobody does.

Two things now raise the stakes. Telemetry is about to go everywhere rather than
three places ([platform#31](0031-telemetry-is-ambient-in-context.md)). And expert
mode ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md)) will
**render log lines into a browser** — so what was a local file readable by whoever
owns the host becomes content served over a session to an authenticated user.

## Decision

**The redaction class is the PII boundary, enforced at construction. There is no
scrubbing pipeline.**

- **Redaction happens when the `Field` is built, not when it is exported.** A
  redacted value is replaced before the entry exists, so the sensitive string is
  never buffered, never queued, never written to a partition, and never present
  in a heap dump of the telemetry subsystem. This is the property a
  post-hoc scrubber cannot offer at any price.
- **`Field` generalises from `string` to `any`.** Today every value must be
  pre-formatted into a string, which is the friction that pushes people toward
  `fmt.Sprintf`. Counts, durations, booleans and error categories should be typed
  in the output, and typed values are also what a structured store can index
  ([platform#36](0036-telemetry-storage-retention-and-expert-mode.md)).
- **A fourth class, `Identifier`, is added: stably hashed rather than dropped.**
  `Sensitive` answers "is this safe to show" but destroys the ability to ask "did
  the same user hit this twice". `Identifier` emits a truncated HMAC under a
  per-install salt, so occurrences correlate within an install and mean nothing
  outside one. Usernames, session references, IP addresses and device identifiers
  are its intended use.
- **The message is a constant; the data is in fields.** `Info("stream closed",
  Sensitive("session", id))`, never `Info(fmt.Sprintf("stream %s closed", id))`.
  A value interpolated into the message text has bypassed classification
  entirely, which is how every fail-closed scheme is actually defeated in
  practice.
- **That rule is enforced, not documented.** A vet-style check in
  `tools/` — alongside the existing `licenseheader` tool and run by the same CI
  job — rejects a non-constant message argument at a telemetry call. The
  `licenseheader` precedent is exact: a rule nobody can forget because the build
  fails.
- **`Secret` stays absolute.** It is redacted in every sink, in every mode, at
  every level, and no expert-mode toggle or debug level reveals it. A resolved
  Secret Broker value must never reach any other constructor, and the fail-closed
  default means a mistake there is a redaction rather than a disclosure.
- **The vocabulary crosses both boundaries it has to.** The SDK mirrors these
  four classes so a module classifies its own values
  ([sdk#5](https://github.com/mosaic-media/sdk/blob/main/docs/adr/0005-modules-observe-through-the-sdk.md)), and the Supervisor uses
  the same ones so one reader parses both processes' records
  ([supervisor#5](https://github.com/mosaic-media/supervisor/blob/main/docs/adr/0005-the-supervisor-observes-independently.md)). A containment
  property that stops at a repository boundary is not a containment property —
  third-party module code is exactly where an unclassified value is most likely
  to originate, and it is text an administrator's browser will render.

## Alternatives considered

**Regex scrubbing at export.** *Rejected*, as above: it guesses, it misses
domain-specific data it has no way to recognise, and it acts after the value has
already been assembled and moved.

**An allowlist of field names known to be safe.** *Rejected.* It has the same
fail-closed property, which is the good part, but it moves the decision away from
the call site into a list maintained elsewhere, so a reader of the code cannot
tell what a line will emit. `String()` at the call site is self-documenting.

**Struct tags on domain types.** *Rejected.* It works for serialising whole
objects and telemetry does not log whole objects — it logs a handful of chosen
values, often derived. It would also put a telemetry concern into `domain`, which
[platform#31](0031-telemetry-is-ambient-in-context.md) forbids.

**Drop `Sensitive` values entirely rather than adding `Identifier`.**
*Rejected.* It is what happens today and it is why the health-check path
special-cases an empty reason to avoid a misleading `[REDACTED]`. Correlating a
user's actions is a legitimate operational need; storing their username to do it
is not, and a salted hash separates the two.

## Consequences

- **Rendering telemetry to a browser is safe by construction.** Expert mode does
  not need its own redaction pass, because there is nothing left to redact by
  the time a record is stored. That is what makes
  [platform#36](0036-telemetry-storage-retention-and-expert-mode.md)'s UI
  defensible rather than alarming.
- **Developers must classify, every time.** This is friction and it is the point.
  It is one word per field, at the moment the answer is obvious.
- **The salt is install-scoped and must not rotate casually.** Rotating it breaks
  correlation across the rotation boundary. It belongs in the Secret Broker, is
  generated on first run, and survives restarts and Generations.
- **A hashed identifier is pseudonymous, not anonymous.** With a small user set
  and the salt, occurrences can be re-linked to a person. It is treated as
  personal data for retention and access purposes — it reduces exposure, it does
  not eliminate the obligation.
- **The support bundle gets stronger.** `diagnostics`'s anonymisation currently
  drops classified values; with `Identifier` it can keep correlation while
  remaining shareable, which makes a bundle materially more useful to whoever
  receives it.

## Implementation implications

`Field` moves from `internal/platform/diagnostics` to
`internal/platform/telemetry`, generalises its value to `any`, and gains
`Identifier`. `domain.RedactionClass` gains the matching constant, so event
payloads and log fields keep one vocabulary. `diagnostics` imports the new home
for its support-bundle path. The vet-style checker is a new tool in `tools/`
wired into `.github/workflows/verify.yml` beside the licence-header check. The
salt is a Secret Broker entry created on first run.
