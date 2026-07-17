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
	ListUnpublished(ctx context.Context, limit int) ([]domain.OutboxEvent, error)
	MarkPublished(ctx context.Context, id domain.EventID) error
}
