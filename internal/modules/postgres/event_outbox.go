package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// eventOutbox is the PostgreSQL contracts.EventOutbox. Append is written to
// run inside the same UnitOfWork transaction as the state change it records,
// so state and event commit atomically (MEG-015 §05). The worker that reads
// unpublished rows and publishes them is a later slice; this slice provides
// persistence only.
type eventOutbox struct {
	q queryer
}

// NewEventOutbox builds a pool-backed EventOutbox. In practice Append runs
// through a UnitOfWork; the direct constructor exists for read-side callers
// (the future outbox worker) and tests.
func NewEventOutbox(pool *pgxpool.Pool) contracts.EventOutbox {
	return &eventOutbox{q: pool}
}

func (o *eventOutbox) Append(ctx context.Context, event domain.OutboxEvent) error {
	_, err := o.q.Exec(ctx,
		`INSERT INTO event_outbox (id, type, payload, occurred_at, published_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		string(event.ID), event.Type, event.Payload, event.OccurredAt, event.PublishedAt,
	)
	if err != nil {
		return mapError("append outbox event", err)
	}
	return nil
}

func (o *eventOutbox) ListUnpublished(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	if limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}
	rows, err := o.q.Query(ctx,
		`SELECT id, type, payload, occurred_at, published_at
		   FROM event_outbox
		  WHERE published_at IS NULL
		  ORDER BY occurred_at, id
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, mapError("list unpublished events", err)
	}
	defer rows.Close()

	var events []domain.OutboxEvent
	for rows.Next() {
		var (
			event       domain.OutboxEvent
			id          string
			publishedAt *time.Time
		)
		if err := rows.Scan(&id, &event.Type, &event.Payload, &event.OccurredAt, &publishedAt); err != nil {
			return nil, mapError("scan outbox event", err)
		}
		event.ID = domain.EventID(id)
		event.PublishedAt = publishedAt
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate outbox events", err)
	}
	return events, nil
}

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
