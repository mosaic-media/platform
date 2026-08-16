// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package jobs

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// Schedule is a recurring job: one enqueued per interval, forever.
//
// The interval is an interval and not a cron expression — see the package
// comment. What it buys is that the occurrence is computable: the slot a
// moment falls in is the moment truncated to the interval, which is what makes
// enqueuing idempotent without a "last run" row to race over.
type Schedule struct {
	Kind        string
	Every       time.Duration
	Payload     []byte
	MaxAttempts int
}

// slot is the occurrence now belongs to — the key an enqueue is idempotent on.
//
// Truncation is against the Unix epoch in UTC, so every process in a
// deployment computes the same boundary regardless of its local zone. Two
// runners at the same instant produce one key and therefore one job.
func (s Schedule) slot(now time.Time) time.Time {
	if s.Every <= 0 {
		return now.UTC()
	}
	return now.UTC().Truncate(s.Every)
}

// ScheduleKey names one occurrence of one schedule.
func (s Schedule) ScheduleKey(now time.Time) string {
	return s.Kind + "@" + s.slot(now).Format(time.RFC3339)
}

// SchedulerDeps are what a Scheduler is built from.
type SchedulerDeps struct {
	Store     contracts.JobStore
	Clock     contracts.Clock
	IDs       contracts.IDGenerator
	Schedules []Schedule
	// Interval is how often due occurrences are enqueued. It bounds how late a
	// schedule can fire, so it should be well under the shortest Every. Zero
	// takes DefaultScheduleInterval.
	Interval time.Duration
}

// DefaultScheduleInterval is how often the scheduler checks for a new
// occurrence. Far more often than the shortest schedule needs, which is the
// point: a missed tick costs nothing because the next one enqueues the same
// slot under the same key.
const DefaultScheduleInterval = time.Minute

// Scheduler enqueues each schedule's current occurrence.
//
// It holds no state between ticks and no "last fired" record. That is what
// makes it survive a restart with no recovery step: on boot it enqueues the
// current slot, which either creates the row or collides with the one already
// there. A Platform that was down for an hour comes back and runs the sweep it
// is now due for, rather than replaying the ones it missed — which is right for
// a maintenance sweep and would be wrong for a schedule that must not skip.
// Nothing queued for this needs the second behaviour; when something does, it
// is a catch-up policy on Schedule, not a rewrite of this.
type Scheduler struct {
	store     contracts.JobStore
	clock     contracts.Clock
	ids       contracts.IDGenerator
	schedules []Schedule
	interval  time.Duration
}

// NewScheduler builds a Scheduler over deps.
func NewScheduler(deps SchedulerDeps) *Scheduler {
	s := &Scheduler{
		store:     deps.Store,
		clock:     deps.Clock,
		ids:       deps.IDs,
		schedules: deps.Schedules,
		interval:  deps.Interval,
	}
	if s.interval <= 0 {
		s.interval = DefaultScheduleInterval
	}
	return s
}

// Start ticks until ctx ends, enqueuing immediately so a boot does not wait a
// whole interval before the queue has anything in it.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			if _, err := s.Tick(ctx); err != nil {
				telemetry.From(ctx).For("jobs").Error("enqueuing scheduled jobs failed", telemetry.Err(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Tick enqueues the current occurrence of every schedule, returning how many
// jobs it actually created. A tick that creates nothing is the normal case:
// most ticks fall inside a slot already queued.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	now := s.clock.Now()
	created := 0
	var firstErr error
	for _, sched := range s.schedules {
		maxAttempts := sched.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = DefaultMaxAttempts
		}
		job := domain.Job{
			ID:          domain.JobID(s.ids.NewID()),
			Kind:        sched.Kind,
			Payload:     sched.Payload,
			Status:      domain.JobPending,
			MaxAttempts: maxAttempts,
			CreatedAt:   now,
			// Runnable at the slot boundary rather than at now, so a job
			// enqueued halfway through a slot still belongs to that slot and
			// is claimable at once.
			ScheduledAt: sched.slot(now),
			ScheduleKey: sched.ScheduleKey(now),
		}
		_, isNew, err := s.store.Enqueue(ctx, job)
		if err != nil {
			// One schedule failing must not stop the others: they are
			// independent, and a store error is usually transient.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if isNew {
			created++
			telemetry.From(ctx).For("jobs").Info("scheduled job enqueued",
				telemetry.String("kind", sched.Kind),
				telemetry.String("occurrence", sched.slot(now).Format(time.RFC3339)))
		}
	}
	return created, firstErr
}
