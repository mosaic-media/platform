package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// EventHandler processes a published event. It returns an error when the
// event could not be handled so the publisher can apply its retry policy.
type EventHandler func(ctx context.Context, event domain.Event) error

// Subscription represents an active EventPublisher subscription.
type Subscription interface {
	Unsubscribe()
}

// EventPublisher provides runtime event dispatch (MEG-015 §03). The
// publisher does not know who is listening; it merely announces that
// something happened (MAC-001 §06).
type EventPublisher interface {
	Publish(ctx context.Context, event domain.Event) error
	Subscribe(eventType string, handler EventHandler) (Subscription, error)
}
