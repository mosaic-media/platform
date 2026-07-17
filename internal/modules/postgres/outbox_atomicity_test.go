package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/modules/postgres"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// TestOutboxStateAtomicOnMidTransactionFailure is the MEG-015 §12 Outbox exit
// criterion — "Command persists state and event atomically" — proven the way
// that actually demonstrates atomicity: a failure is injected mid-transaction,
// AFTER both the domain state write and the outbox append but BEFORE commit,
// and neither row is allowed to persist.
//
// It queries the pool directly (bypassing the stores) so the assertion cannot
// be fooled by store-level filtering — it inspects the raw tables.
func TestOutboxStateAtomicOnMidTransactionFailure(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	uow := postgres.NewUnitOfWork(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	injected := errors.New("injected failure after outbox append, before commit")

	err := uow.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
		// State write.
		if _, err := tx.Users().Create(c, domain.User{
			ID: "u-atomic", Username: "atomic", Email: "atomic@example.com", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		// Outbox append in the SAME transaction.
		if err := tx.Outbox().Append(c, domain.OutboxEvent{Event: domain.Event{
			ID: "e-atomic", Type: "user.created", OccurredAt: now, RecordedAt: now, Payload: []byte("atomic"),
		}}); err != nil {
			return err
		}
		// Fail after both writes but before the UnitOfWork commits.
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("WithinTx error = %v, want injected", err)
	}

	// Direct table inspection: NEITHER row may exist.
	var users int
	if err := pool.QueryRow(c, `SELECT count(*) FROM users WHERE id = 'u-atomic'`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 0 {
		t.Fatalf("user row must not persist after rollback, found %d", users)
	}
	var events int
	if err := pool.QueryRow(c, `SELECT count(*) FROM event_outbox WHERE id = 'e-atomic'`).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 0 {
		t.Fatalf("outbox row must not persist after rollback, found %d", events)
	}
}

// TestOutboxRecordedAtPersistsFullEnvelope confirms the §06 envelope columns
// are written to and read from the real event_outbox table, including the
// failure-bookkeeping columns being present with their defaults.
func TestOutboxRecordedAtPersistsFullEnvelope(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	outbox := postgres.NewEventOutbox(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := outbox.Append(c, domain.OutboxEvent{Event: domain.Event{
		ID: "e-1", Type: "t.v1", OccurredAt: now, RecordedAt: now.Add(time.Second),
		Actor: "user-1", TenantScope: "local", CorrelationID: "corr", CausationID: "cause",
		Payload: []byte("data"), RedactionClass: domain.RedactionNone,
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Read the envelope columns straight from the table.
	var (
		actor, tenant, corr, cause, redaction string
		recordedAt                            time.Time
		deadLettered                          bool
		attempts                              int
	)
	if err := pool.QueryRow(c,
		`SELECT actor, tenant_scope, correlation_id, causation_id, redaction_class,
		        recorded_at, dead_lettered, attempts
		   FROM event_outbox WHERE id = 'e-1'`,
	).Scan(&actor, &tenant, &corr, &cause, &redaction, &recordedAt, &deadLettered, &attempts); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	if actor != "user-1" || tenant != "local" || corr != "corr" || cause != "cause" || redaction != string(domain.RedactionNone) {
		t.Fatalf("envelope columns wrong: actor=%q tenant=%q corr=%q cause=%q redaction=%q", actor, tenant, corr, cause, redaction)
	}
	if !recordedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("recorded_at = %v, want %v", recordedAt, now.Add(time.Second))
	}
	if deadLettered || attempts != 0 {
		t.Fatalf("fresh event should have dead_lettered=false attempts=0, got %v/%d", deadLettered, attempts)
	}
}
