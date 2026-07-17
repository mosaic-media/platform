package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// Bus is the Platform's in-process Event Bus (MEG-015 §06 — First Event
// Model). It implements contracts.EventPublisher and
// contracts.ComponentHealthReporter (MEG-015 §09 — Diagnostics Model).
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

	component string
	clock     contracts.Clock

	healthMu            sync.Mutex
	lastState           domain.HealthState
	degradedReason      string
	lastSuccessfulCheck time.Time
	lastFailureCategory string
}

// BusOption configures a Bus.
type BusOption func(*Bus)

// WithBusComponent overrides the component identifier Bus reports itself
// under (MEG-015 §09 — Diagnostics Model).
func WithBusComponent(component string) BusOption {
	return func(b *Bus) { b.component = component }
}

// WithBusClock overrides the clock used for health bookkeeping timestamps.
// Tests use this for a deterministic LastSuccessfulCheck.
func WithBusClock(clock contracts.Clock) BusOption {
	return func(b *Bus) { b.clock = clock }
}

// NewBus returns an empty in-process Event Bus.
func NewBus(opts ...BusOption) *Bus {
	b := &Bus{
		subscribers: make(map[string][]*subscription),
		component:   "event-bus",
		clock:       systemClock{},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
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
// the Bus doc comment for delivery and error semantics. Every call updates
// the health bookkeeping ReportHealth reads.
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
		joined := errors.Join(failures...)
		reason := fmt.Sprintf("%d subscriber(s) failed for event type %q", len(failures), event.Type)
		b.recordCheck(domain.HealthDegraded, string(contracts.CategoryOf(joined)), reason)
		return joined
	}

	b.recordCheck(domain.HealthHealthy, "", "")
	return nil
}

// recordCheck updates the health bookkeeping ReportHealth reads.
func (b *Bus) recordCheck(state domain.HealthState, failureCategory, reason string) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	b.lastState = state
	b.degradedReason = reason
	if failureCategory != "" {
		b.lastFailureCategory = failureCategory
	}
	if state == domain.HealthHealthy {
		b.lastSuccessfulCheck = b.clock.Now()
	}
}

// ReportHealth implements contracts.ComponentHealthReporter. Unlike Worker,
// Bus has no external dependency to confirm before it can claim health: an
// in-process dispatcher that has never been asked to publish anything has
// no evidence of failure, so it reports Healthy by default rather than
// Unavailable.
func (b *Bus) ReportHealth(context.Context) domain.ComponentHealth {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	health := b.lastState
	if health == "" {
		health = domain.HealthHealthy
	}

	return domain.ComponentHealth{
		Component:           b.component,
		Lifecycle:           domain.LifecycleRunning,
		Health:              health,
		DegradedReason:      b.degradedReason,
		LastSuccessfulCheck: b.lastSuccessfulCheck,
		LastFailureCategory: b.lastFailureCategory,
		// DegradedReason may echo a subscriber's internal error detail, so
		// it defaults to redacted in a support bundle rather than assumed
		// safe.
		RedactionClass: domain.RedactionSensitive,
	}
}
