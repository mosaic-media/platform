package domain

import "time"

// RedactionClass classifies how an event's payload should be treated in
// diagnostics and support bundles (MEG-015 §06 envelope field
// "redaction_class"; MEG-015 §09 — Diagnostics: "support bundle redaction
// class"). The specs reference the field without enumerating values yet, so
// this is a first cut with a redact-by-default bias: support bundles must be
// "fully anonymised" (§09), and audit payloads "must avoid credential
// material and must be compatible with redacted diagnostics" (§07). A fuller
// taxonomy can extend this when the Diagnostics slice lands.
type RedactionClass string

const (
	// RedactionNone marks a payload safe to include verbatim in a support
	// bundle: it carries no personal data or secrets.
	RedactionNone RedactionClass = "none"
	// RedactionSensitive marks a payload that carries personal or identifying
	// data and must be redacted from support bundles. This is the safe
	// default for events whose class is not stated.
	RedactionSensitive RedactionClass = "sensitive"
	// RedactionSecret marks a payload that carries credential or secret
	// material and must never appear in diagnostics. Audit payloads should
	// never be this class (§07); it exists as an explicit guard.
	RedactionSecret RedactionClass = "secret"
)

// Event is a Platform domain event and its envelope. Modules own event
// meaning; the Platform owns the envelope and routing (MAC-001 §06). The
// fields are the event envelope defined in MEG-015 §06.
type Event struct {
	// ID is the stable event identity (envelope: event_id).
	ID EventID
	// Type is the versioned event name (envelope: event_type).
	Type string
	// OccurredAt is the domain occurrence time (envelope: occurred_at).
	OccurredAt time.Time
	// RecordedAt is the Platform persistence time (envelope: recorded_at).
	// It is stamped when the Platform records the event into the outbox; in
	// synchronous command handling it coincides with OccurredAt.
	RecordedAt time.Time
	// Actor is the authenticated subject or system actor that produced the
	// event (envelope: actor). Empty for anonymous or pre-authentication
	// events.
	Actor string
	// TenantScope is the local server or workspace scope (envelope:
	// tenant_scope). Empty means the single local scope.
	TenantScope string
	// CorrelationID ties the event to a request or job (envelope:
	// correlation_id). Empty until request-scoped propagation exists.
	CorrelationID string
	// CausationID is the prior event or command that caused this one
	// (envelope: causation_id). Empty when there is no prior cause.
	CausationID string
	// Payload is the event-specific data (envelope: payload).
	Payload []byte
	// RedactionClass governs diagnostics/support-bundle handling of Payload
	// (envelope: redaction_class).
	RedactionClass RedactionClass
}

// OutboxEvent is an Event persisted through the transactional outbox
// pattern, alongside the state change that produced it, pending asynchronous
// publication. It carries the delivery bookkeeping MEG-015 §06 — Failure
// Behaviour requires the outbox worker to track. The worker itself is a
// later slice; this type and the fields exist so the write path and failure
// bookkeeping are real now.
type OutboxEvent struct {
	Event

	// PublishedAt is set once the event has been published to subscribers.
	PublishedAt *time.Time

	// Attempts counts delivery attempts made so far (§06: attempt count).
	Attempts int
	// LastErrorCategory is the Platform error category of the most recent
	// failed delivery, empty if none (§06: last error category).
	LastErrorCategory string
	// NextRetryAt is when the next delivery attempt becomes due, nil if not
	// scheduled (§06: next retry time).
	NextRetryAt *time.Time
	// DeadLettered marks an event abandoned after exhausting retries (§06:
	// dead-letter status). A dead-lettered event is never published.
	DeadLettered bool
	// OwningComponent is the component responsible for the event's delivery,
	// used to route failures (§06: owning component).
	OwningComponent string
}

// Published reports whether this event has been published.
func (e OutboxEvent) Published() bool {
	return e.PublishedAt != nil
}
