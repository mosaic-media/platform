// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// EventHandler processes a published event. It returns an error when the
// event could not be handled so the publisher can apply its retry policy.
//
// Delivery is at-least-once: the outbox worker retries a failed delivery,
// and a retry redelivers the SAME event to EVERY subscriber registered for
// its type, not just the one that failed.
// A handler MUST therefore be idempotent — safe to receive the same event
// more than once, including receiving it again after it already completed
// successfully. The Platform provides no deduplication in this first
// implementation; callers own their own idempotency (for example, tracking
// processed event IDs before applying a side effect).
//
// A handler that mutates persistent state MUST do so through an application
// service or an explicit handler service with its own UnitOfWork. It MUST
// NOT write to a store directly, bypassing the command boundary defined by
// the command order — doing so would let events skip validation, authorization
// and the transactional guarantees every other write path relies on.
type EventHandler func(ctx context.Context, event domain.Event) error

// Subscription represents an active EventPublisher subscription.
type Subscription interface {
	// Unsubscribe cancels the subscription so its handler receives no further events.
	Unsubscribe()
}

// EventPublisher provides runtime event dispatch. The publisher does not
// know who is listening; it merely announces that something happened. See
// EventHandler for the at-least-once delivery and idempotency guarantees this
// implies for subscribers.
type EventPublisher interface {
	// Publish dispatches an event to every handler subscribed to its type.
	Publish(ctx context.Context, event domain.Event) error
	// Subscribe registers a handler for events of the given type and returns a
	// Subscription that cancels the registration when unsubscribed.
	Subscribe(eventType string, handler EventHandler) (Subscription, error)
}
