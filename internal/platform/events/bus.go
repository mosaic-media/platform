package events

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// Bus is the Platform's in-process Event Bus (MEG-015 §06 — First Event
// Model). It implements contracts.EventPublisher.
//
// Publish dispatches event to every subscriber registered for event.Type,
// synchronously, in registration order. The publisher does not know or care
// who is listening (MAC-001 §06): publishing an event nobody subscribed to
// is not an error.
//
// If ANY subscriber handler returns an error, Publish returns a non-nil
// error built from all of the failing handlers' errors — it does not
// silently drop the failure or continue as if delivery succeeded. Bus
// itself does not retry; that is the outbox worker's job (Worker in this
// package), and a worker-driven retry redelivers the event to EVERY
// subscriber of that type again, not just the one that failed. This is
// exactly why contracts.EventHandler requires idempotency: a handler that
// already completed its side effect may be invoked again for the same
// event.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string][]*subscription
	nextID      uint64
}

// NewBus returns an empty in-process Event Bus.
func NewBus() *Bus {
	return &Bus{subscribers: make(map[string][]*subscription)}
}

type subscription struct {
	id        uint64
	eventType string
	handler   contracts.EventHandler
	bus       *Bus
}

func (s *subscription) Unsubscribe() {
	s.bus.unsubscribe(s.eventType, s.id)
}

// Subscribe registers handler for events of eventType. The returned
// Subscription's Unsubscribe stops further delivery to handler; it does not
// affect deliveries already in progress.
func (b *Bus) Subscribe(eventType string, handler contracts.EventHandler) (contracts.Subscription, error) {
	if eventType == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "event type is required")
	}
	if handler == nil {
		return nil, contracts.NewError(contracts.InvalidArgument, "handler is required")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	sub := &subscription{id: b.nextID, eventType: eventType, handler: handler, bus: b}
	b.subscribers[eventType] = append(b.subscribers[eventType], sub)
	return sub, nil
}

func (b *Bus) unsubscribe(eventType string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subscribers[eventType]
	for i, sub := range subs {
		if sub.id == id {
			b.subscribers[eventType] = append(subs[:i], subs[i+1:]...)
			return
		}
	}
}

// Publish delivers event to every subscriber registered for event.Type. See
// the Bus doc comment for delivery and error semantics.
func (b *Bus) Publish(ctx context.Context, event domain.Event) error {
	b.mu.RLock()
	subs := append([]*subscription(nil), b.subscribers[event.Type]...)
	b.mu.RUnlock()

	var failures []error
	for _, sub := range subs {
		if err := sub.handler(ctx, event); err != nil {
			failures = append(failures, fmt.Errorf("subscriber for event type %q: %w", event.Type, err))
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return nil
}
