// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/jobs"
)

func newScheduler(store *fakeStore, clock *fakeClock, schedules ...jobs.Schedule) *jobs.Scheduler {
	return jobs.NewScheduler(jobs.SchedulerDeps{
		Store: store, Clock: clock, IDs: &fakeIDs{}, Schedules: schedules,
	})
}

// The property the whole scheduler rests on: ticking repeatedly inside one
// occurrence creates one job. Without it, an hourly sweep with a minute-long
// tick would enqueue sixty.
func TestSchedulerEnqueuesOneJobPerOccurrence(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}
	s := newScheduler(store, clock, jobs.Schedule{Kind: "sweep", Every: time.Hour})

	if created, err := s.Tick(context.Background()); err != nil || created != 1 {
		t.Fatalf("first Tick = %d, %v; want 1, nil", created, err)
	}
	for i := 0; i < 5; i++ {
		clock.advance(10 * time.Minute)
		created, err := s.Tick(context.Background())
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if created != 0 {
			t.Fatalf("tick inside the same hour created %d jobs, want 0", created)
		}
	}

	// The next hour is a new occurrence.
	clock.advance(11 * time.Minute)
	if created, err := s.Tick(context.Background()); err != nil || created != 1 {
		t.Fatalf("next-hour Tick = %d, %v; want 1, nil", created, err)
	}

	all, _ := store.List(context.Background(), domain.JobFilter{})
	if len(all) != 2 {
		t.Fatalf("enqueued %d jobs across two hours, want 2", len(all))
	}
}

// Survives a restart, the scheduler's half: a fresh Scheduler holds no state,
// so a boot inside an occurrence already swept collides rather than re-running
// it, and a boot in a new occurrence enqueues at once instead of waiting out an
// interval.
func TestASecondSchedulerAfterARestartDoesNotDuplicateTheOccurrence(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}
	first := newScheduler(store, clock, jobs.Schedule{Kind: "sweep", Every: time.Hour})
	if created, _ := first.Tick(context.Background()); created != 1 {
		t.Fatal("the first scheduler enqueued nothing")
	}

	clock.advance(20 * time.Minute)
	afterRestart := newScheduler(store, clock, jobs.Schedule{Kind: "sweep", Every: time.Hour})
	if created, err := afterRestart.Tick(context.Background()); err != nil || created != 0 {
		t.Fatalf("a restart inside the occurrence created %d jobs, want 0", created)
	}

	clock.advance(time.Hour)
	if created, err := afterRestart.Tick(context.Background()); err != nil || created != 1 {
		t.Fatalf("a restart in a new occurrence created %d jobs, want 1", created)
	}
}

// The slot is the truncated instant, so the key is stable across processes and
// zones rather than depending on when a particular one happened to boot.
func TestScheduleKeyIsStableWithinAnOccurrence(t *testing.T) {
	s := jobs.Schedule{Kind: "sweep", Every: time.Hour}
	early := s.ScheduleKey(time.Date(2026, 7, 26, 9, 0, 1, 0, time.UTC))
	late := s.ScheduleKey(time.Date(2026, 7, 26, 9, 59, 59, 0, time.UTC))
	if early != late {
		t.Fatalf("keys differ inside one hour: %q vs %q", early, late)
	}
	// A local-zone instant naming the same absolute moment must agree, or two
	// processes in different zones would each enqueue their own copy.
	zone := time.FixedZone("UTC+7", 7*3600)
	if got := s.ScheduleKey(time.Date(2026, 7, 26, 16, 30, 0, 0, zone)); got != early {
		t.Fatalf("a non-UTC clock produced %q, want %q", got, early)
	}
	if next := s.ScheduleKey(time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)); next == early {
		t.Fatal("the next hour reused the previous occurrence's key")
	}
}

// A schedule enqueues its job runnable at the slot boundary, so a process that
// boots halfway through an occurrence runs the sweep straight away rather than
// waiting for the next one.
func TestAScheduledJobIsRunnableAsSoonAsItIsEnqueued(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow.Add(37 * time.Minute)}
	s := newScheduler(store, clock, jobs.Schedule{Kind: "sweep", Every: time.Hour})
	if _, err := s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	runner := newRunner(t, store, clock)
	ran := 0
	runner.Register("sweep", func(context.Context, domain.Job) error { ran++; return nil })
	if n, err := runner.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want the scheduled job to be claimable at once", n, err)
	}
	if ran != 1 {
		t.Fatalf("handler ran %d times, want 1", ran)
	}
}
