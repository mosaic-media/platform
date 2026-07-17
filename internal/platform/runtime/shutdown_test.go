package runtime_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/events"
	"github.com/mosaic-media/mosaic-platform/internal/platform/runtime"
)

// TestShutdownDrainsInFlightOutboxEventBeforeReturning is the MEG-015 §12
// Supervisor exit criterion, proven by simulation rather than by merely
// asserting the hook exists: the worker's background poll loop is given a
// one-hour interval, so its own ticker cannot possibly fire during this
// test. An event is appended while the worker is "running" (in-flight
// outbox work), then Shutdown is called. If Shutdown did not perform its
// own final drain, the event would still be unpublished when this test
// checks — the only way it gets checkpointed here is Shutdown's own doing.
func TestShutdownDrainsInFlightOutboxEventBeforeReturning(t *testing.T) {
	outbox := newFakeOutbox()
	bus := events.NewBus()
	var delivered int32
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		atomic.AddInt32(&delivered, 1)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker", events.WithPollInterval(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)

	// Simulate a command committing state+event while the worker is
	// running — in-flight outbox work waiting to be drained.
	if err := outbox.Append(context.Background(), domain.OutboxEvent{
		Event: domain.Event{ID: "e-1", Type: "t", Payload: []byte("x")},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	lifecycle := runtime.NewLifecycle()
	lifecycle.MarkRunning()
	result := runtime.Shutdown(context.Background(), lifecycle, worker)

	if result.FinalDrainErr != nil {
		t.Fatalf("FinalDrainErr = %v", result.FinalDrainErr)
	}
	if result.FinalDrainPublished != 1 {
		t.Fatalf("FinalDrainPublished = %d, want 1 — Shutdown must checkpoint pending outbox work, not leave it for a poll tick that (at a one-hour interval) will not come", result.FinalDrainPublished)
	}
	if atomic.LoadInt32(&delivered) != 1 {
		t.Fatalf("delivered = %d, want 1: the event must actually reach the subscriber as part of Shutdown, not just be marked drained", delivered)
	}
	if outbox.publishedCount() != 1 {
		t.Fatalf("publishedCount() = %d, want 1", outbox.publishedCount())
	}
	if lifecycle.State() != domain.LifecycleStopped {
		t.Fatalf("Lifecycle = %q, want %q after Shutdown", lifecycle.State(), domain.LifecycleStopped)
	}
}

func TestShutdownWithoutAPriorStartStillDrains(t *testing.T) {
	outbox := newFakeOutbox()
	bus := events.NewBus()
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := outbox.Append(context.Background(), domain.OutboxEvent{
		Event: domain.Event{ID: "e-1", Type: "t", Payload: []byte("x")},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker")
	lifecycle := runtime.NewLifecycle()

	// Worker.Start was never called (mirrors the current boot-time-only
	// RunOnce usage) — Stop must be a safe no-op and Shutdown must still
	// perform the final drain.
	result := runtime.Shutdown(context.Background(), lifecycle, worker)
	if result.FinalDrainPublished != 1 {
		t.Fatalf("FinalDrainPublished = %d, want 1", result.FinalDrainPublished)
	}
}
