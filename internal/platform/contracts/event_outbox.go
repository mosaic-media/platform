package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// EventOutbox provides commit-time event persistence (MEG-015 §03). Append
// is called within the same UnitOfWork transaction as the state change it
// records, so state and event commit atomically.
type EventOutbox interface {
	Append(ctx context.Context, event domain.OutboxEvent) error

	// ListUnpublished returns unpublished, non-dead-lettered events that are
	// currently deliverable, oldest occurrence first: an event with a
	// NextRetryAt in the future (still waiting out its backoff window after a
	// prior failure — MEG-015 §06 — Failure Behaviour) must not be returned
	// until that time passes. This is what lets the outbox worker poll
	// repeatedly without hot-looping a retry before its backoff elapses.
	ListUnpublished(ctx context.Context, limit int) ([]domain.OutboxEvent, error)

	MarkPublished(ctx context.Context, id domain.EventID) error

	// RecordFailure records that a delivery attempt for the event failed with
	// the given Platform error category, attributed to component. The
	// implementation increments the attempt count and applies the Platform
	// delivery policy to set the next retry time or dead-letter the event
	// (MEG-015 §06 — Failure Behaviour). The outbox worker that performs
	// deliveries and calls this is a later slice; this is the bookkeeping the
	// worker will drive, not delivery itself.
	RecordFailure(ctx context.Context, id domain.EventID, category ErrorCategory, component string) error
}
