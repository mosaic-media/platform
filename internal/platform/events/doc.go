// Package events holds the Platform's in-process Event Bus and the outbox
// worker that drains committed outbox rows into it (MEG-015 §06 — Event
// Backbone; MEG-015 §12 — "In-process Event Bus" slice, exit criterion
// "Outbox worker publishes to an idempotent local subscriber").
//
// Bus implements contracts.EventPublisher: it dispatches a published event
// to every subscriber registered for that event's type. Worker implements
// the other half of §06's First Event Model — it polls
// contracts.EventOutbox.ListUnpublished, publishes each deliverable event
// through an EventPublisher, and on success marks it published or on
// failure records the failure so the Platform delivery policy can schedule
// a retry or dead-letter the event.
//
// Delivery is at-least-once: see contracts.EventHandler for what that
// requires of subscribers. Nothing in this package enforces the command-
// boundary rule that state-mutating handlers must go through an
// application service — that is a contract for handler authors, documented
// on contracts.EventHandler, not something Go's type system can check.
package events
