// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/events"
)

var testNow = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

func testEvent(id domain.EventID, eventType string) domain.Event {
	return domain.Event{ID: id, Type: eventType, OccurredAt: testNow, RecordedAt: testNow, Payload: []byte("payload")}
}

func TestBusPublishDeliversToSubscribersOfMatchingType(t *testing.T) {
	bus := events.NewBus()

	var mu sync.Mutex
	var received []domain.EventID
	_, err := bus.Subscribe("user.created", func(_ context.Context, event domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, event.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), testEvent("e-1", "user.created")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// A different event type must not reach this subscriber.
	if err := bus.Publish(context.Background(), testEvent("e-2", "user.deleted")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0] != "e-1" {
		t.Fatalf("received = %v, want [e-1]", received)
	}
}

func TestBusPublishWithNoSubscribersIsNotAnError(t *testing.T) {
	bus := events.NewBus()
	if err := bus.Publish(context.Background(), testEvent("e-1", "nobody.listening")); err != nil {
		t.Fatalf("Publish() with no subscribers = %v, want nil", err)
	}
}

func TestBusPublishDeliversToEverySubscriberOfType(t *testing.T) {
	bus := events.NewBus()

	var mu sync.Mutex
	calls := map[string]int{}
	record := func(name string) contracts.EventHandler {
		return func(context.Context, domain.Event) error {
			mu.Lock()
			defer mu.Unlock()
			calls[name]++
			return nil
		}
	}
	if _, err := bus.Subscribe("t", record("a")); err != nil {
		t.Fatalf("Subscribe a: %v", err)
	}
	if _, err := bus.Subscribe("t", record("b")); err != nil {
		t.Fatalf("Subscribe b: %v", err)
	}

	if err := bus.Publish(context.Background(), testEvent("e-1", "t")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls["a"] != 1 || calls["b"] != 1 {
		t.Fatalf("calls = %v, want a=1 b=1", calls)
	}
}

func TestBusUnsubscribeStopsFurtherDelivery(t *testing.T) {
	bus := events.NewBus()

	var count int
	sub, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), testEvent("e-1", "t")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	sub.Unsubscribe()
	if err := bus.Publish(context.Background(), testEvent("e-2", "t")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if count != 1 {
		t.Fatalf("count = %d, want 1 (no delivery after Unsubscribe)", count)
	}
}

func TestBusPublishReturnsErrorWhenASubscriberFails(t *testing.T) {
	bus := events.NewBus()
	sentinel := errors.New("handler exploded")

	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		return sentinel
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	err := bus.Publish(context.Background(), testEvent("e-1", "t"))
	if err == nil {
		t.Fatal("expected Publish to report the subscriber failure, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Publish() error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestBusPublishReportsFailureEvenWhenOtherSubscribersSucceed(t *testing.T) {
	bus := events.NewBus()
	sentinel := errors.New("handler b exploded")

	var aCalled bool
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		aCalled = true
		return nil
	}); err != nil {
		t.Fatalf("Subscribe a: %v", err)
	}
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		return sentinel
	}); err != nil {
		t.Fatalf("Subscribe b: %v", err)
	}

	err := bus.Publish(context.Background(), testEvent("e-1", "t"))
	if !aCalled {
		t.Fatal("expected the succeeding subscriber to still be invoked")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Publish() error = %v, want it to wrap %v", err, sentinel)
	}
}

// TestSubscriberIdempotencyAcrossDuplicatePublish is the exit-criteria test:
// a deliberately idempotent subscriber, published the same event twice,
// produces no duplicate side effect. At-least-once delivery means a
// subscriber WILL see the same event more than once — via worker
// retries, or here, via a direct duplicate Publish simulating that — and it
// must handle that safely.
func TestSubscriberIdempotencyAcrossDuplicatePublish(t *testing.T) {
	bus := events.NewBus()

	sideEffects := 0
	processed := map[domain.EventID]bool{}
	var mu sync.Mutex

	idempotentHandler := func(_ context.Context, event domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if processed[event.ID] {
			// Already applied this event's effect: safe no-op, not an error.
			return nil
		}
		processed[event.ID] = true
		sideEffects++
		return nil
	}
	if _, err := bus.Subscribe("user.created", idempotentHandler); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	event := testEvent("e-1", "user.created")
	if err := bus.Publish(context.Background(), event); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if err := bus.Publish(context.Background(), event); err != nil {
		t.Fatalf("second Publish (duplicate delivery): %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if sideEffects != 1 {
		t.Fatalf("side effects = %d, want 1 (duplicate delivery must not duplicate the effect)", sideEffects)
	}
}
