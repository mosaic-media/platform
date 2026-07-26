// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The half of the jobs runner PostgreSQL decides rather than the Platform:
// `FOR UPDATE SKIP LOCKED` handing two runners disjoint sets, a lapsed lease
// becoming claimable again, and the partial unique index that makes a recurring
// enqueue idempotent. None of these can be demonstrated against a fake — a fake
// would only prove the test agrees with itself about what the SQL means.

func jobsStore(t *testing.T) (contracts.JobStore, context.Context) {
	t.Helper()
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return postgres.NewJobStore(pool), ctx
}

func pendingJob(id, kind string, at time.Time) domain.Job {
	return domain.Job{
		ID: domain.JobID(id), Kind: kind, Status: domain.JobPending,
		MaxAttempts: 3, CreatedAt: at,
	}
}

func TestClaimSkipsLockedRowsSoTwoRunnersNeverTakeTheSameJob(t *testing.T) {
	store, ctx := jobsStore(t)
	now := time.Now().UTC()

	for _, id := range []string{"job-a", "job-b", "job-c", "job-d"} {
		if _, _, err := store.Enqueue(ctx, pendingJob(id, "work", now)); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
	}

	// Two claims in a row, each limited to two. Disjointness is what SKIP
	// LOCKED buys; the alternative — FOR UPDATE alone — would have the second
	// runner block behind the first, turning a pool of workers into one worker
	// with spectators.
	first, err := store.Claim(ctx, now, "runner-a", time.Minute, 2)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	second, err := store.Claim(ctx, now, "runner-b", time.Minute, 2)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("claimed %d then %d, want 2 and 2", len(first), len(second))
	}
	seen := map[domain.JobID]string{}
	for _, j := range append(append([]domain.Job{}, first...), second...) {
		if owner, dup := seen[j.ID]; dup {
			t.Fatalf("%s was claimed twice (by %s and %s)", j.ID, owner, j.LeasedBy)
		}
		seen[j.ID] = j.LeasedBy
		if j.Status != domain.JobRunning {
			t.Fatalf("%s claimed with status %s, want %s", j.ID, j.Status, domain.JobRunning)
		}
		if j.Attempt != 1 {
			t.Fatalf("%s claimed at attempt %d, want 1", j.ID, j.Attempt)
		}
	}

	// Nothing left: a claimed job is not claimable again while its lease holds.
	third, err := store.Claim(ctx, now, "runner-c", time.Minute, 4)
	if err != nil {
		t.Fatalf("third Claim: %v", err)
	}
	if len(third) != 0 {
		t.Fatalf("claimed %d already-leased jobs", len(third))
	}
}

// Survives a restart, against the real predicate: the lease is what makes an
// abandoned job recoverable rather than stuck in `running` for good.
func TestAnExpiredLeaseBecomesClaimableAgain(t *testing.T) {
	store, ctx := jobsStore(t)
	now := time.Now().UTC()
	if _, _, err := store.Enqueue(ctx, pendingJob("job-abandoned", "work", now)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if claimed, err := store.Claim(ctx, now, "boot-1", time.Minute, 1); err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}
	// Process dies here: no Complete, no Retry, no DeadLetter.

	inside, err := store.Claim(ctx, now.Add(30*time.Second), "boot-2", time.Minute, 1)
	if err != nil {
		t.Fatalf("Claim inside lease: %v", err)
	}
	if len(inside) != 0 {
		t.Fatalf("a live lease was stolen by %s", inside[0].LeasedBy)
	}

	after, err := store.Claim(ctx, now.Add(2*time.Minute), "boot-2", time.Minute, 1)
	if err != nil {
		t.Fatalf("Claim after lease: %v", err)
	}
	if len(after) != 1 {
		t.Fatal("an expired lease was not reclaimed")
	}
	if after[0].LeasedBy != "boot-2" {
		t.Fatalf("reclaimed by %q, want boot-2", after[0].LeasedBy)
	}
	if after[0].Attempt != 2 {
		t.Fatalf("attempt = %d, want 2 — an abandoned claim still costs one", after[0].Attempt)
	}
}

// The property the recurring schedule rests on, in the database rather than in
// the scheduler: the same occurrence enqueued twice is one row.
func TestEnqueueIsIdempotentOnTheScheduleKey(t *testing.T) {
	store, ctx := jobsStore(t)
	now := time.Now().UTC()

	job := pendingJob("job-1", "sweep", now)
	job.ScheduleKey = "sweep@2026-07-26T12:00:00Z"
	stored, created, err := store.Enqueue(ctx, job)
	if err != nil || !created {
		t.Fatalf("first Enqueue = %v, %v, %v", stored, created, err)
	}

	// A different id, the same occurrence — which is exactly what a second
	// process, or the same one after a restart, produces.
	duplicate := pendingJob("job-2", "sweep", now)
	duplicate.ScheduleKey = job.ScheduleKey
	existing, created, err := store.Enqueue(ctx, duplicate)
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if created {
		t.Fatal("the same occurrence was enqueued twice")
	}
	if existing.ID != job.ID {
		t.Fatalf("the collision returned %q, want the existing %q", existing.ID, job.ID)
	}

	all, err := store.List(ctx, domain.JobFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("%d rows for one occurrence, want 1", len(all))
	}
}

// A job enqueued by hand carries no key, and several must be allowed to
// coexist — the reason the unique index is partial.
func TestSeveralKeylessJobsCanCoexist(t *testing.T) {
	store, ctx := jobsStore(t)
	now := time.Now().UTC()
	for _, id := range []string{"job-1", "job-2", "job-3"} {
		if _, created, err := store.Enqueue(ctx, pendingJob(id, "work", now)); err != nil || !created {
			t.Fatalf("Enqueue %s = %v, %v", id, created, err)
		}
	}
	all, _ := store.List(ctx, domain.JobFilter{})
	if len(all) != 3 {
		t.Fatalf("%d keyless jobs stored, want 3", len(all))
	}
}

// A future job is not runnable, which is what a retry's backoff depends on.
func TestClaimIgnoresAJobScheduledForLater(t *testing.T) {
	store, ctx := jobsStore(t)
	now := time.Now().UTC()
	job := pendingJob("job-later", "work", now)
	job.ScheduledAt = now.Add(time.Hour)
	if _, _, err := store.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if claimed, err := store.Claim(ctx, now, "runner", time.Minute, 4); err != nil || len(claimed) != 0 {
		t.Fatalf("Claim = %d jobs, %v; want none before the scheduled time", len(claimed), err)
	}
	if claimed, err := store.Claim(ctx, now.Add(time.Hour), "runner", time.Minute, 4); err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %d jobs, %v; want 1 once due", len(claimed), err)
	}
}

// The lifecycle writes, read back through the queries the expert-mode screens
// use. A dead-letter that lost its history would be a failure nobody can
// diagnose, which is the whole reason the runner records rather than only logs.
func TestAttemptsAndLogsAreReadableAfterTheJobEnds(t *testing.T) {
	store, ctx := jobsStore(t)
	now := time.Now().UTC()
	id := domain.JobID("job-history")
	if _, _, err := store.Enqueue(ctx, pendingJob(string(id), "work", now)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		claimed, err := store.Claim(ctx, now, "runner", time.Minute, 1)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("Claim %d = %v, %v", attempt, claimed, err)
		}
		a := domain.JobAttempt{
			ID: domain.JobAttemptID("attempt-" + string(rune('0'+attempt))), JobID: id,
			Attempt: attempt, StartedAt: now, Runner: "runner",
		}
		if _, err := store.StartAttempt(ctx, a); err != nil {
			t.Fatalf("StartAttempt: %v", err)
		}
		a.FinishedAt = now.Add(time.Second)
		a.Duration = time.Second
		a.Status = domain.JobAttemptFailed
		a.Error = "it did not work"
		if err := store.FinishAttempt(ctx, a); err != nil {
			t.Fatalf("FinishAttempt: %v", err)
		}
		if err := store.AppendLog(ctx, domain.JobLog{
			ID: domain.JobLogID("log-" + string(rune('0'+attempt))), JobID: id,
			LoggedAt: now.Add(time.Duration(attempt) * time.Second), Level: "warn", Message: "attempt failed",
		}); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
		if attempt == 1 {
			if err := store.Retry(ctx, id, now, "it did not work"); err != nil {
				t.Fatalf("Retry: %v", err)
			}
		}
	}
	if err := store.DeadLetter(ctx, id, now, "it did not work"); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}

	job, err := store.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if job.Status != domain.JobDead {
		t.Fatalf("status = %s, want %s", job.Status, domain.JobDead)
	}
	if job.FinishedAt.IsZero() {
		t.Fatal("a dead-lettered job recorded no finish time")
	}
	attempts, err := store.Attempts(ctx, id)
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("%d attempts recorded, want 2", len(attempts))
	}
	if attempts[0].Duration != time.Second || attempts[0].Error == "" {
		t.Fatalf("attempt 1 lost its outcome: %+v", attempts[0])
	}
	lines, err := store.Logs(ctx, id, 0)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("%d log lines, want 2", len(lines))
	}

	// A dead job is not claimed again, which is what dead-lettering is for.
	if claimed, err := store.Claim(ctx, now.Add(time.Hour), "runner", time.Minute, 4); err != nil || len(claimed) != 0 {
		t.Fatalf("a dead-lettered job was claimed again: %d, %v", len(claimed), err)
	}
}

func TestListFiltersByKindAndStatus(t *testing.T) {
	store, ctx := jobsStore(t)
	now := time.Now().UTC()
	for _, spec := range []struct{ id, kind string }{
		{"job-a", "sweep"}, {"job-b", "sweep"}, {"job-c", "import"},
	} {
		if _, _, err := store.Enqueue(ctx, pendingJob(spec.id, spec.kind, now)); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if err := store.DeadLetter(ctx, "job-b", now, "gave up"); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}

	sweeps, err := store.List(ctx, domain.JobFilter{Kind: "sweep"})
	if err != nil || len(sweeps) != 2 {
		t.Fatalf("kind filter returned %d, %v; want 2", len(sweeps), err)
	}
	dead, err := store.List(ctx, domain.JobFilter{Status: domain.JobDead})
	if err != nil || len(dead) != 1 {
		t.Fatalf("status filter returned %d, %v; want 1", len(dead), err)
	}
	if dead[0].ID != "job-b" {
		t.Fatalf("status filter returned %q", dead[0].ID)
	}
}
