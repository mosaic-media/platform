// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// EventOutbox provides commit-time event persistence. Append is called
// within the same UnitOfWork transaction as the state change it records, so
// state and event commit atomically.
type EventOutbox interface {
	// Append records an event in the outbox within the current transaction, so
	// it commits atomically with the state change it accompanies.
	Append(ctx context.Context, event domain.OutboxEvent) error

	// ListUnpublished returns unpublished, non-dead-lettered events that are
	// currently deliverable, oldest occurrence first: an event with a
	// NextRetryAt in the future (still waiting out its backoff window after a
	// prior failure) must not be returned until that time passes. This is what
	// lets the outbox worker poll repeatedly without hot-looping a retry before
	// its backoff elapses.
	ListUnpublished(ctx context.Context, limit int) ([]domain.OutboxEvent, error)

	// MarkPublished marks an event as successfully delivered so it is no longer
	// returned by ListUnpublished.
	MarkPublished(ctx context.Context, id domain.EventID) error

	// RecordFailure records that a delivery attempt for the event failed with
	// the given Platform error category, attributed to component. The
	// implementation increments the attempt count and applies the Platform
	// delivery policy to set the next retry time or dead-letter the event.
	RecordFailure(ctx context.Context, id domain.EventID, category ErrorCategory, component string) error
}
