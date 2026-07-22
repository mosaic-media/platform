// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// redactedPlaceholder replaces any Field value not explicitly marked safe.
const redactedPlaceholder = "[REDACTED]"

// identifierPrefix marks a value as a salted digest rather than the thing it
// stands for, so nobody reads one as a real username or session reference.
const identifierPrefix = "id:"

// Field is one structured field of a record. Redaction is enforced twice, on
// purpose (ADR 0056):
//
//   - **At construction.** Sensitive, Secret and Identifier never carry the
//     original value forward — it is replaced or digested before the Field
//     exists. So the sensitive string is never buffered, never queued, never
//     written to a sink and never present in a heap dump of this package. A
//     scrubber that runs at export cannot offer that at any price, because by
//     then the value has already travelled.
//   - **At emit.** Any Field whose Redaction is not domain.RedactionNone is
//     replaced again on the way out. This catches the case construction cannot:
//     a Field built as a struct literal rather than through a constructor. Its
//     Redaction is then the zero value, which is not RedactionNone, so it fails
//     closed — a field somebody forgot to classify is redacted, not leaked.
//
// The second check is what makes the first safe to rely on rather than a
// convention that holds until someone writes Field{Key: …, Value: …} by hand.
type Field struct {
	Key       string
	Value     any
	Redaction domain.RedactionClass
}

// String builds a Field written verbatim — for values that are always safe:
// component and module identifiers, states, error categories, counts. Never
// use it for anything that could be personal data or a secret.
func String(key, value string) Field {
	return Field{Key: key, Value: value, Redaction: domain.RedactionNone}
}

// Int builds a verbatim Field for a count or size. Counts describe volume, not
// people, so they are safe by construction.
func Int(key string, value int) Field {
	return Field{Key: key, Value: value, Redaction: domain.RedactionNone}
}

// Int64 builds a verbatim Field for a 64-bit count, size or offset.
func Int64(key string, value int64) Field {
	return Field{Key: key, Value: value, Redaction: domain.RedactionNone}
}

// Bool builds a verbatim Field for a flag.
func Bool(key string, value bool) Field {
	return Field{Key: key, Value: value, Redaction: domain.RedactionNone}
}

// Duration builds a verbatim Field for an elapsed time, rendered as the
// familiar "1.5s" rather than a raw nanosecond count so a human reading the
// console sink does not have to divide.
func Duration(key string, value time.Duration) Field {
	return Field{Key: key, Value: value.String(), Redaction: domain.RedactionNone}
}

// Err builds a verbatim Field carrying an error's message. Error text is
// authored by the Platform and its dependencies rather than by a user, so it
// is treated as safe — with the standing caveat that an error which
// interpolates user input into its message has smuggled that input past this
// classification, which is a bug in the error, not in this call.
func Err(err error) Field {
	if err == nil {
		return Field{Key: "error", Value: "", Redaction: domain.RedactionNone}
	}
	return Field{Key: "error", Value: err.Error(), Redaction: domain.RedactionNone}
}

// Sensitive builds a Field for a value that may carry personal or identifying
// data. The value is dropped at construction; only the key survives, so the
// record still shows that the field was present.
func Sensitive(key string, value any) Field {
	return Field{Key: key, Value: redactAtConstruction(value), Redaction: domain.RedactionSensitive}
}

// Secret builds a Field for a value that may carry credential or secret
// material. A resolved Secret Broker value must only ever reach this
// constructor, if it must be logged at all — and it is dropped here rather
// than carried and masked later.
func Secret(key string, value any) Field {
	return Field{Key: key, Value: redactAtConstruction(value), Redaction: domain.RedactionSecret}
}

// Identifier builds a Field carrying a stable salted digest of value rather
// than value itself. It answers "is this the same subject as before" without
// storing who the subject is: two records for one user share a digest, and the
// digest means nothing outside this install.
//
// It is pseudonymous, not anonymous. With the salt and a small user set a
// digest can be re-linked to a person, so an Identifier field is still treated
// as personal data for retention and access purposes. It reduces exposure; it
// does not remove the obligation.
func Identifier(key string, value any) Field {
	if isEmpty(value) {
		return Field{Key: key, Value: "", Redaction: domain.RedactionIdentifier}
	}
	return Field{
		Key:       key,
		Value:     identifierPrefix + digest(fmt.Sprint(value)),
		Redaction: domain.RedactionIdentifier,
	}
}

// redactAtConstruction drops a classified value. An empty value has nothing to
// redact, so it stays empty rather than becoming a misleading "[REDACTED]" —
// which is what a healthy component's reason-less health check would otherwise
// show.
func redactAtConstruction(value any) any {
	if isEmpty(value) {
		return ""
	}
	return redactedPlaceholder
}

// isEmpty reports whether value carries nothing worth redacting.
func isEmpty(value any) bool {
	if value == nil {
		return true
	}
	s, ok := value.(string)
	return ok && s == ""
}

// emitValue returns what a sink should write for f. It re-applies redaction on
// the way out so a hand-built Field literal — whose zero-value Redaction is not
// RedactionNone — fails closed, and so an Identifier's digest (already computed
// at construction) passes through unchanged.
func (f Field) emitValue() any {
	switch f.Redaction {
	case domain.RedactionNone, domain.RedactionIdentifier:
		return f.Value
	default:
		return redactAtConstruction(f.Value)
	}
}

// salt is the per-install HMAC key behind Identifier. It is generated once per
// process today, which means digests correlate within a run and not across a
// restart. Persisting it through the Secret Broker — so correlation survives a
// restart, and rotating it is a deliberate act rather than an accident of
// lifecycle — is the intended home and is not built; ADR 0056 records that.
var (
	saltOnce  sync.Once
	saltValue []byte
)

// SetIdentifierSalt fixes the salt explicitly. It must be called before the
// first Identifier field is built — a test uses it for determinism, and the
// composition root will use it once the Secret Broker holds the value. Calling
// it after a digest has been produced does nothing, because a salt that
// changed mid-process would silently split one subject into two.
func SetIdentifierSalt(s []byte) {
	saltOnce.Do(func() {
		saltValue = append([]byte(nil), s...)
	})
}

// digest returns a truncated HMAC of value under the install salt. Truncation
// to 16 hex characters keeps records readable; the collision risk across the
// number of subjects a Mosaic install has is not a practical concern.
func digest(value string) string {
	saltOnce.Do(func() {
		saltValue = make([]byte, 32)
		if _, err := rand.Read(saltValue); err != nil {
			// A failure here must not take the process down over a log field.
			// A zero salt still produces stable digests within the run; it only
			// means they are not unguessable, which is a weaker property than
			// intended and better than a crash.
			saltValue = make([]byte, 32)
		}
	})
	mac := hmac.New(sha256.New, saltValue)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// ComponentHealthFields builds the Field set for a domain.ComponentHealth.
// Identifiers and states are structural facts and always safe; DegradedReason
// is free text a reporter attached, so it is classified by the health's own
// RedactionClass — exactly as a support bundle treats it.
func ComponentHealthFields(health domain.ComponentHealth) []Field {
	fields := []Field{
		String("lifecycle", string(health.Lifecycle)),
		String("health", string(health.Health)),
		String("last_failure_category", health.LastFailureCategory),
	}
	switch health.RedactionClass {
	case domain.RedactionNone:
		fields = append(fields, String("degraded_reason", health.DegradedReason))
	case domain.RedactionSecret:
		fields = append(fields, Secret("degraded_reason", health.DegradedReason))
	default:
		fields = append(fields, Sensitive("degraded_reason", health.DegradedReason))
	}
	return fields
}
