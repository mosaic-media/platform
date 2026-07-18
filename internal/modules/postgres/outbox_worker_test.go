package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/modules/postgres"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/events"
)

// TestOutboxRecordFailurePersistsExactBookkeeping verifies, via raw SQL
// against the real table, that RecordFailure writes exactly the bookkeeping
// MEG-015 §06 — Failure Behaviour requires: attempt count, last error
// category, next retry time and owning component. The reusable contract
// suite (test/contract/suite.go) only asserts the behavioural consequence
// (the event stops being immediately deliverable); this is where the exact
// column values are checked, because EventOutbox has no adapter-agnostic
// read path for them by design.
func TestOutboxRecordFailurePersistsExactBookkeeping(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	outbox := postgres.NewEventOutbox(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := outbox.Append(c, domain.OutboxEvent{Event: domain.Event{
		ID: "e-1", Type: "t", OccurredAt: now, RecordedAt: now, Payload: []byte("p"),
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	before := time.Now().UTC()
	if err := outbox.RecordFailure(c, "e-1", contracts.Unavailable, "outbox-worker"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	var (
		attempts        int
		lastErrCategory string
		owningComponent string
		deadLettered    bool
		nextRetryAt     *time.Time
	)
	if err := pool.QueryRow(c,
		`SELECT attempts, last_error_category, owning_component, dead_lettered, next_retry_at
		   FROM event_outbox WHERE id = 'e-1'`,
	).Scan(&attempts, &lastErrCategory, &owningComponent, &deadLettered, &nextRetryAt); err != nil {
		t.Fatalf("read failure bookkeeping: %v", err)
	}

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if lastErrCategory != string(contracts.Unavailable) {
		t.Fatalf("last_error_category = %q, want %q", lastErrCategory, contracts.Unavailable)
	}
	if owningComponent != "outbox-worker" {
		t.Fatalf("owning_component = %q, want outbox-worker", owningComponent)
	}
	if deadLettered {
		t.Fatal("dead_lettered should be false after a single failure")
	}
	if nextRetryAt == nil {
		t.Fatal("next_retry_at must be set after a retryable failure")
	}
	// domain.DefaultDeliveryPolicy's base delay is one minute; allow slack
	// for test execution time either side without asserting an exact value.
	if nextRetryAt.Before(before.Add(30 * time.Second)) {
		t.Fatalf("next_retry_at = %v is too soon after failure at %v", nextRetryAt, before)
	}
}

// TestOutboxListUnpublishedBecomesDeliverableOnceRetryIsDue proves the
// due-time gating added to ListUnpublished against the real table: a
// retrying event is excluded while next_retry_at is in the future, and
// reappears once it is not. The test manipulates next_retry_at directly via
// SQL to simulate elapsed time — real PostgreSQL's now() cannot be
// fast-forwarded, and this is exactly the WHERE clause under test, not
// RecordFailure's scheduling logic (already covered above).
func TestOutboxListUnpublishedBecomesDeliverableOnceRetryIsDue(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	outbox := postgres.NewEventOutbox(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := outbox.Append(c, domain.OutboxEvent{Event: domain.Event{
		ID: "e-1", Type: "t", OccurredAt: now, RecordedAt: now, Payload: []byte("p"),
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := outbox.RecordFailure(c, "e-1", contracts.Internal, "outbox-worker"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	deliverable, err := outbox.ListUnpublished(c, 10)
	if err != nil {
		t.Fatalf("ListUnpublished (retry not yet due): %v", err)
	}
	if len(deliverable) != 0 {
		t.Fatalf("expected the event to be excluded while its retry is not due, got %d", len(deliverable))
	}

	// Simulate the backoff having elapsed.
	if _, err := pool.Exec(c,
		`UPDATE event_outbox SET next_retry_at = $1 WHERE id = 'e-1'`,
		time.Now().UTC().Add(-time.Second),
	); err != nil {
		t.Fatalf("simulate elapsed backoff: %v", err)
	}

	deliverable, err = outbox.ListUnpublished(c, 10)
	if err != nil {
		t.Fatalf("ListUnpublished (retry due): %v", err)
	}
	if len(deliverable) != 1 || deliverable[0].ID != "e-1" {
		t.Fatalf("expected the event to reappear once its retry is due, got %+v", deliverable)
	}
}

// TestWorkerPublishesFromRealPostgresOutboxToIdempotentSubscriber is the
// MEG-015 §12 exit criterion — "Outbox worker publishes to an idempotent
// local subscriber" — proven end to end against a real database: a command
// commits state and an outbox row in the same transaction (proving
// atomicity again, incidentally), then a real events.Worker over the real
// events.Bus drains it. The subscriber tracks processed event IDs and is
// invoked twice with the same event id to confirm duplicate delivery does
// not duplicate its side effect.
func TestWorkerPublishesFromRealPostgresOutboxToIdempotentSubscriber(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Simulate a command committing state and an outbox event atomically,
	// resolving both stores through the uniform Store[T] mechanism (MAD-001).
	if err := cs.UnitOfWork.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
		users, err := contracts.Store[contracts.UserStore](tx)
		if err != nil {
			return err
		}
		outbox, err := contracts.Store[contracts.EventOutbox](tx)
		if err != nil {
			return err
		}
		if _, err := users.Create(c, domain.User{
			ID: "u-1", Username: "worker-test", Email: "worker-test@example.com", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return outbox.Append(c, domain.OutboxEvent{Event: domain.Event{
			ID: "e-1", Type: "user.created", OccurredAt: now, RecordedAt: now, Payload: []byte("worker-test"),
		}})
	}); err != nil {
		t.Fatalf("commit state+event: %v", err)
	}

	bus := events.NewBus()
	sideEffects := 0
	processed := map[domain.EventID]bool{}
	if _, err := bus.Subscribe("user.created", func(_ context.Context, event domain.Event) error {
		if processed[event.ID] {
			return nil // idempotent: already applied
		}
		processed[event.ID] = true
		sideEffects++
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(cs.Outbox, bus, "outbox-worker")

	published, err := worker.RunOnce(c)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1", published)
	}
	if sideEffects != 1 {
		t.Fatalf("side effects = %d, want 1", sideEffects)
	}

	// Directly redeliver the same event to the subscriber (simulating a
	// worker retry / at-least-once redelivery) and confirm idempotency.
	if err := bus.Publish(c, domain.Event{ID: "e-1", Type: "user.created", OccurredAt: now, RecordedAt: now}); err != nil {
		t.Fatalf("duplicate Publish: %v", err)
	}
	if sideEffects != 1 {
		t.Fatalf("side effects after duplicate delivery = %d, want still 1", sideEffects)
	}

	var publishedAt *time.Time
	if err := pool.QueryRow(c, `SELECT published_at FROM event_outbox WHERE id = 'e-1'`).Scan(&publishedAt); err != nil {
		t.Fatalf("read published_at: %v", err)
	}
	if publishedAt == nil {
		t.Fatal("event should be marked published in the real table")
	}
}
