package domain

import "time"

// Event is a Platform domain event. Modules own event meaning; the Platform
// owns the envelope and routing (MAC-001 §06).
type Event struct {
	ID         EventID
	Type       string
	Payload    []byte
	OccurredAt time.Time
}

// OutboxEvent is an Event persisted through the transactional outbox
// pattern, alongside the state change that produced it, pending
// asynchronous publication.
type OutboxEvent struct {
	Event
	PublishedAt *time.Time
}

// Published reports whether this event has been published.
func (e OutboxEvent) Published() bool {
	return e.PublishedAt != nil
}
