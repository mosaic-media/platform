// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// eventOutbox is the PostgreSQL contracts.EventOutbox. Append is written to
// run inside the same UnitOfWork transaction as the state change it records,
// so state and event commit atomically. It persists the full event envelope
// and the delivery failure-tracking columns; the worker that reads
// unpublished rows, publishes them and drives RecordFailure is a later slice.
type eventOutbox struct {
	q      queryer
	policy domain.DeliveryPolicy
}

// NewEventOutbox builds a pool-backed EventOutbox. In practice Append runs
// through a UnitOfWork; the direct constructor exists for read-side callers
// (the future outbox worker) and tests.
func NewEventOutbox(pool *pgxpool.Pool) contracts.EventOutbox {
	return &eventOutbox{q: pool, policy: domain.DefaultDeliveryPolicy()}
}

const eventOutboxColumns = `id, type, occurred_at, recorded_at, actor, tenant_scope,
	correlation_id, causation_id, payload, redaction_class,
	published_at, attempts, last_error_category, next_retry_at, dead_lettered, owning_component`

// Append persists one outbox event, defaulting an unclassified payload to the
// sensitive redaction class so it commits atomically with the state change.
func (o *eventOutbox) Append(ctx context.Context, event domain.OutboxEvent) error {
	redaction := event.RedactionClass
	if redaction == "" {
		// Redact by default when a producer does not classify the payload —
		// support bundles must be redacted-safe.
		redaction = domain.RedactionSensitive
	}
	_, err := o.q.Exec(ctx,
		`INSERT INTO event_outbox
		   (id, type, occurred_at, recorded_at, actor, tenant_scope,
		    correlation_id, causation_id, payload, redaction_class,
		    published_at, attempts, last_error_category, next_retry_at, dead_lettered, owning_component)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		string(event.ID), event.Type, event.OccurredAt, event.RecordedAt, event.Actor, event.TenantScope,
		event.CorrelationID, event.CausationID, event.Payload, string(redaction),
		event.PublishedAt, event.Attempts, event.LastErrorCategory, event.NextRetryAt, event.DeadLettered, event.OwningComponent,
	)
	if err != nil {
		return mapError("append outbox event", err)
	}
	return nil
}

// ListUnpublished returns unpublished, non-dead-lettered, currently
// deliverable events oldest first. Dead-lettered events are excluded because
// they will never be published; an event still waiting out its retry
// backoff (next_retry_at in the future) is excluded until it becomes due,
// using the event_outbox_deliverable_idx partial index (migration 0009).
func (o *eventOutbox) ListUnpublished(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	if limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}
	rows, err := o.q.Query(ctx,
		`SELECT `+eventOutboxColumns+`
		   FROM event_outbox
		  WHERE published_at IS NULL AND dead_lettered = false
		    AND (next_retry_at IS NULL OR next_retry_at <= $2)
		  ORDER BY occurred_at, id
		  LIMIT $1`,
		limit, time.Now().UTC(),
	)
	if err != nil {
		return nil, mapError("list unpublished events", err)
	}
	defer rows.Close()

	var events []domain.OutboxEvent
	for rows.Next() {
		event, err := scanOutboxEvent(rows)
		if err != nil {
			return nil, mapError("scan outbox event", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate outbox events", err)
	}
	return events, nil
}

// MarkPublished stamps an event as published, treating a re-publish as
// idempotent and a missing event as NotFound.
func (o *eventOutbox) MarkPublished(ctx context.Context, id domain.EventID) error {
	now := time.Now().UTC()
	tag, err := o.q.Exec(ctx,
		`UPDATE event_outbox SET published_at = $2 WHERE id = $1 AND published_at IS NULL`,
		string(id), now,
	)
	if err != nil {
		return mapError("mark event published", err)
	}
	if tag.RowsAffected() == 0 {
		// Absent or already published: confirm existence so a missing event is
		// reported as NotFound while a re-publish is treated as idempotent.
		var exists bool
		if err := o.q.QueryRow(ctx, `SELECT true FROM event_outbox WHERE id = $1`, string(id)).Scan(&exists); err != nil {
			if isNoRows(err) {
				return contracts.NewError(contracts.NotFound, "outbox event not found")
			}
			return mapError("confirm outbox event", err)
		}
	}
	return nil
}

// RecordFailure increments the attempt count for an event, records the error
// category and owning component, and applies the delivery policy to either
// schedule the next retry or dead-letter the event. It is
// idempotent-safe against a missing event (NotFound) and never touches an
// already-published or already-dead-lettered event.
func (o *eventOutbox) RecordFailure(ctx context.Context, id domain.EventID, category contracts.ErrorCategory, component string) error {
	// Read the current attempt count under the caller's transaction (or the
	// pool) so the policy decision uses an up-to-date value.
	var attempts int
	err := o.q.QueryRow(ctx,
		`SELECT attempts FROM event_outbox
		  WHERE id = $1 AND published_at IS NULL AND dead_lettered = false`,
		string(id),
	).Scan(&attempts)
	if err != nil {
		if isNoRows(err) {
			// Either the event does not exist, or it is already published or
			// dead-lettered — in all cases there is nothing to record.
			var exists bool
			if e := o.q.QueryRow(ctx, `SELECT true FROM event_outbox WHERE id = $1`, string(id)).Scan(&exists); e != nil {
				if isNoRows(e) {
					return contracts.NewError(contracts.NotFound, "outbox event not found")
				}
				return mapError("confirm outbox event for failure", e)
			}
			// Terminal state (published or dead-lettered): treat as a no-op.
			return nil
		}
		return mapError("read attempts for failure", err)
	}

	attempts++
	nextRetry, deadLettered := o.policy.Schedule(attempts, time.Now().UTC())

	var nextRetryArg *time.Time
	if !deadLettered {
		nextRetryArg = &nextRetry
	}

	_, err = o.q.Exec(ctx,
		`UPDATE event_outbox
		    SET attempts = $2,
		        last_error_category = $3,
		        owning_component = $4,
		        next_retry_at = $5,
		        dead_lettered = $6
		  WHERE id = $1`,
		string(id), attempts, string(category), component, nextRetryArg, deadLettered,
	)
	if err != nil {
		return mapError("record outbox failure", err)
	}
	return nil
}

func scanOutboxEvent(row pgx.Row) (domain.OutboxEvent, error) {
	var (
		event       domain.OutboxEvent
		id          string
		redaction   string
		publishedAt *time.Time
		nextRetryAt *time.Time
	)
	if err := row.Scan(
		&id, &event.Type, &event.OccurredAt, &event.RecordedAt, &event.Actor, &event.TenantScope,
		&event.CorrelationID, &event.CausationID, &event.Payload, &redaction,
		&publishedAt, &event.Attempts, &event.LastErrorCategory, &nextRetryAt, &event.DeadLettered, &event.OwningComponent,
	); err != nil {
		return domain.OutboxEvent{}, err
	}
	event.ID = domain.EventID(id)
	event.RedactionClass = domain.RedactionClass(redaction)
	event.PublishedAt = publishedAt
	event.NextRetryAt = nextRetryAt
	return event, nil
}
