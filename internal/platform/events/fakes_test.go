// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package events_test

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// testClock is a mutable clock so retry-backoff tests can advance time
// deterministically instead of sleeping.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(start time.Time) *testClock {
	return &testClock{now: start}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeOutbox is an in-memory contracts.EventOutbox that mirrors the
// PostgreSQL adapter's due-time semantics: ListUnpublished excludes
// dead-lettered events and events still waiting out a retry backoff,
// exactly like the real implementation's next_retry_at filter. It is driven
// by an injectable clock so tests can advance past a backoff window without
// sleeping.
type fakeOutbox struct {
	mu     sync.Mutex
	clock  *testClock
	policy domain.DeliveryPolicy
	events map[domain.EventID]domain.OutboxEvent
}

func newFakeOutbox(clock *testClock) *fakeOutbox {
	return &fakeOutbox{
		clock:  clock,
		policy: domain.DefaultDeliveryPolicy(),
		events: make(map[domain.EventID]domain.OutboxEvent),
	}
}

func (o *fakeOutbox) Append(_ context.Context, event domain.OutboxEvent) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events[event.ID] = event
	return nil
}

func (o *fakeOutbox) ListUnpublished(_ context.Context, limit int) ([]domain.OutboxEvent, error) {
	if limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	now := o.clock.Now()
	var deliverable []domain.OutboxEvent
	for _, event := range o.events {
		if event.Published() || event.DeadLettered {
			continue
		}
		if event.NextRetryAt != nil && event.NextRetryAt.After(now) {
			continue
		}
		deliverable = append(deliverable, event)
	}
	sort.Slice(deliverable, func(i, j int) bool {
		if deliverable[i].OccurredAt.Equal(deliverable[j].OccurredAt) {
			return deliverable[i].ID < deliverable[j].ID
		}
		return deliverable[i].OccurredAt.Before(deliverable[j].OccurredAt)
	})
	if len(deliverable) > limit {
		deliverable = deliverable[:limit]
	}
	return deliverable, nil
}

func (o *fakeOutbox) MarkPublished(_ context.Context, id domain.EventID) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	event, ok := o.events[id]
	if !ok {
		return contracts.NewError(contracts.NotFound, "outbox event not found")
	}
	now := o.clock.Now()
	event.PublishedAt = &now
	o.events[id] = event
	return nil
}

func (o *fakeOutbox) RecordFailure(_ context.Context, id domain.EventID, category contracts.ErrorCategory, component string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	event, ok := o.events[id]
	if !ok {
		return contracts.NewError(contracts.NotFound, "outbox event not found")
	}
	event.Attempts++
	event.LastErrorCategory = string(category)
	event.OwningComponent = component

	next, deadLettered := o.policy.Schedule(event.Attempts, o.clock.Now())
	event.DeadLettered = deadLettered
	if deadLettered {
		event.NextRetryAt = nil
	} else {
		event.NextRetryAt = &next
	}
	o.events[id] = event
	return nil
}

// get returns the current state of an event for assertions. It exists only
// on the fake — the real contract has no such read path, by design (see
// test/contract/suite.go's outbox-failure subtest).
func (o *fakeOutbox) get(id domain.EventID) (domain.OutboxEvent, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	event, ok := o.events[id]
	return event, ok
}
