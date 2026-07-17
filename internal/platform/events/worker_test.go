package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/events"
)

func TestWorkerRunOncePublishesAndMarksPublished(t *testing.T) {
	clock := newTestClock(testNow)
	outbox := newFakeOutbox(clock)
	if err := outbox.Append(context.Background(), domain.OutboxEvent{Event: testEvent("e-1", "t")}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	bus := events.NewBus()
	var handled int
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		handled++
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker")
	published, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1", published)
	}
	if handled != 1 {
		t.Fatalf("handled = %d, want 1", handled)
	}

	event, ok := outbox.get("e-1")
	if !ok {
		t.Fatal("event should still exist in the outbox")
	}
	if !event.Published() {
		t.Fatal("event should be marked published")
	}

	// A second RunOnce must not redeliver an already-published event.
	published, err = worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if published != 0 {
		t.Fatalf("second RunOnce published = %d, want 0", published)
	}
	if handled != 1 {
		t.Fatalf("handled after second RunOnce = %d, want still 1", handled)
	}
}

// TestWorkerRunOnceRecordsFailureInsteadOfSilentlyDropping is the exit
// criterion "a subscriber failure results in retry per the failure-tracking
// logic, not silent drop": when the subscriber fails, RunOnce must call
// RecordFailure (proven here via the fake's bookkeeping) rather than
// swallowing the error and marking the event published, or simply losing
// track of it.
func TestWorkerRunOnceRecordsFailureInsteadOfSilentlyDropping(t *testing.T) {
	clock := newTestClock(testNow)
	outbox := newFakeOutbox(clock)
	if err := outbox.Append(context.Background(), domain.OutboxEvent{Event: testEvent("e-1", "t")}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	bus := events.NewBus()
	sentinel := contracts.NewError(contracts.Unavailable, "downstream unreachable")
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		return sentinel
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker")
	published, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if published != 0 {
		t.Fatalf("published = %d, want 0", published)
	}

	event, ok := outbox.get("e-1")
	if !ok {
		t.Fatal("event must still exist in the outbox — it must not be dropped")
	}
	if event.Published() {
		t.Fatal("event must not be marked published after a failed delivery")
	}
	if event.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (RecordFailure must be called on delivery failure)", event.Attempts)
	}
	if event.LastErrorCategory != string(contracts.Unavailable) {
		t.Fatalf("last error category = %q, want %q", event.LastErrorCategory, contracts.Unavailable)
	}
	if event.OwningComponent != "outbox-worker" {
		t.Fatalf("owning component = %q, want outbox-worker", event.OwningComponent)
	}
	if event.NextRetryAt == nil {
		t.Fatal("a retryable failure must schedule a next retry, not leave the event stuck")
	}
	if event.DeadLettered {
		t.Fatal("event must not be dead-lettered after a single failure")
	}
}

func TestWorkerRetriesAfterBackoffElapsesAndSucceeds(t *testing.T) {
	clock := newTestClock(testNow)
	outbox := newFakeOutbox(clock)
	if err := outbox.Append(context.Background(), domain.OutboxEvent{Event: testEvent("e-1", "t")}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	bus := events.NewBus()
	var mu sync.Mutex
	attempt := 0
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		attempt++
		if attempt == 1 {
			return errors.New("transient failure")
		}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker")

	// First attempt fails and schedules a retry roughly one minute out
	// (domain.DefaultDeliveryPolicy's base delay).
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	event, _ := outbox.get("e-1")
	if event.Published() {
		t.Fatal("event must not be published after the first, failing attempt")
	}

	// Polling again before the backoff elapses must not redeliver.
	published, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce before backoff elapsed: %v", err)
	}
	if published != 0 {
		t.Fatalf("published before backoff elapsed = %d, want 0", published)
	}
	mu.Lock()
	attemptsSoFar := attempt
	mu.Unlock()
	if attemptsSoFar != 1 {
		t.Fatalf("handler invocations before backoff elapsed = %d, want 1 (no premature retry)", attemptsSoFar)
	}

	// Advance the clock past the scheduled retry time.
	clock.Advance(2 * time.Minute)

	published, err = worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce after backoff elapsed: %v", err)
	}
	if published != 1 {
		t.Fatalf("published after backoff elapsed = %d, want 1", published)
	}
	event, _ = outbox.get("e-1")
	if !event.Published() {
		t.Fatal("event should be published once the retry succeeds")
	}
}

func TestWorkerDeadLettersAfterMaxAttemptsAndStopsRetrying(t *testing.T) {
	clock := newTestClock(testNow)
	outbox := newFakeOutbox(clock)
	if err := outbox.Append(context.Background(), domain.OutboxEvent{Event: testEvent("e-1", "t")}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	bus := events.NewBus()
	var mu sync.Mutex
	calls := 0
	alwaysFails := errors.New("permanently broken")
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return alwaysFails
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker")
	policy := domain.DefaultDeliveryPolicy()

	for i := 0; i < policy.MaxAttempts; i++ {
		if _, err := worker.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce #%d: %v", i+1, err)
		}
		clock.Advance(time.Hour) // always past whatever backoff was scheduled
	}

	event, ok := outbox.get("e-1")
	if !ok {
		t.Fatal("dead-lettered event must still exist for diagnostics, not be deleted")
	}
	if !event.DeadLettered {
		t.Fatalf("event should be dead-lettered after %d attempts, attempts=%d", policy.MaxAttempts, event.Attempts)
	}

	mu.Lock()
	callsAtDeadLetter := calls
	mu.Unlock()

	// Further polling must not keep retrying a dead-lettered event.
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce after dead-letter: %v", err)
	}
	mu.Lock()
	callsAfter := calls
	mu.Unlock()
	if callsAfter != callsAtDeadLetter {
		t.Fatalf("handler invoked again after dead-letter: calls %d -> %d", callsAtDeadLetter, callsAfter)
	}
}

func TestWorkerStartPublishesInBackgroundThenStopsCleanly(t *testing.T) {
	clock := newTestClock(testNow)
	outbox := newFakeOutbox(clock)
	if err := outbox.Append(context.Background(), domain.OutboxEvent{Event: testEvent("e-1", "t")}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	bus := events.NewBus()
	handled := make(chan domain.EventID, 1)
	if _, err := bus.Subscribe("t", func(_ context.Context, event domain.Event) error {
		handled <- event.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker", events.WithPollInterval(5*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker.Start(ctx)

	select {
	case id := <-handled:
		if id != "e-1" {
			t.Fatalf("handled event id = %q, want e-1", id)
		}
	case <-time.After(2 * time.Second):
		worker.Stop()
		t.Fatal("timed out waiting for the background worker to publish the event")
	}

	worker.Stop()

	event, _ := outbox.get("e-1")
	if !event.Published() {
		t.Fatal("event should be published by the background worker")
	}
}
