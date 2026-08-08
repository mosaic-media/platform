// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package jobs_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/jobs"
)

// The runner's policies — retry with backoff, dead-letter, an unhandled kind,
// a panicking handler — are tested against an in-memory store because they are
// decisions the runner makes, not things PostgreSQL does. The half PostgreSQL
// genuinely decides (SKIP LOCKED disjointness, lease reclaim, the idempotent
// enqueue) is tested against a real engine in the postgres package, where a
// fake would only prove the test agrees with itself.

var testNow = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeIDs struct {
	mu sync.Mutex
	n  int
}

func (g *fakeIDs) NewID() domain.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return domain.ID("id-" + strconv.Itoa(g.n))
}

// fakeStore is an in-memory contracts.JobStore with the same claim semantics
// the SQL has: due pending work, plus running work whose lease has lapsed.
type fakeStore struct {
	mu       sync.Mutex
	jobs     map[domain.JobID]*domain.Job
	order    []domain.JobID
	attempts []domain.JobAttempt
	logs     []domain.JobLog
	claimErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{jobs: map[domain.JobID]*domain.Job{}}
}

func (s *fakeStore) Enqueue(_ context.Context, job domain.Job) (domain.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.ScheduleKey != "" {
		for _, existing := range s.jobs {
			if existing.ScheduleKey == job.ScheduleKey {
				return *existing, false, nil
			}
		}
	}
	stored := job
	s.jobs[job.ID] = &stored
	s.order = append(s.order, job.ID)
	return stored, true, nil
}

func (s *fakeStore) Claim(_ context.Context, now time.Time, owner string, lease time.Duration, limit int) ([]domain.Job, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Job
	for _, id := range s.order {
		if len(out) >= limit {
			break
		}
		j := s.jobs[id]
		runnable := (j.Status == domain.JobPending && (j.ScheduledAt.IsZero() || !j.ScheduledAt.After(now))) ||
			(j.Status == domain.JobRunning && !j.LeasedUntil.IsZero() && !j.LeasedUntil.After(now))
		if !runnable {
			continue
		}
		j.Status = domain.JobRunning
		j.Attempt++
		j.LeasedBy = owner
		j.LeasedUntil = now.Add(lease)
		out = append(out, *j)
	}
	return out, nil
}

func (s *fakeStore) Complete(_ context.Context, id domain.JobID, at time.Time) error {
	return s.finish(id, domain.JobSucceeded, at, "")
}

func (s *fakeStore) DeadLetter(_ context.Context, id domain.JobID, at time.Time, cause string) error {
	return s.finish(id, domain.JobDead, at, cause)
}

func (s *fakeStore) finish(id domain.JobID, status domain.JobStatus, at time.Time, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return contracts.NewError(contracts.NotFound, "job not found")
	}
	j.Status = status
	j.FinishedAt = at
	j.LastError = cause
	j.LeasedBy = ""
	j.LeasedUntil = time.Time{}
	return nil
}

func (s *fakeStore) Retry(_ context.Context, id domain.JobID, at time.Time, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return contracts.NewError(contracts.NotFound, "job not found")
	}
	j.Status = domain.JobPending
	j.ScheduledAt = at
	j.LastError = cause
	j.LeasedBy = ""
	j.LeasedUntil = time.Time{}
	return nil
}

func (s *fakeStore) FindByID(_ context.Context, id domain.JobID) (domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return domain.Job{}, contracts.NewError(contracts.NotFound, "job not found")
	}
	return *j, nil
}

func (s *fakeStore) List(_ context.Context, _ domain.JobFilter) ([]domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Job, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, *s.jobs[id])
	}
	return out, nil
}

func (s *fakeStore) StartAttempt(_ context.Context, a domain.JobAttempt) (domain.JobAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.Status = domain.JobAttemptRunning
	s.attempts = append(s.attempts, a)
	return a, nil
}

func (s *fakeStore) FinishAttempt(_ context.Context, a domain.JobAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.attempts {
		if s.attempts[i].JobID == a.JobID && s.attempts[i].Attempt == a.Attempt {
			s.attempts[i] = a
			return nil
		}
	}
	return contracts.NewError(contracts.NotFound, "job attempt not found")
}

func (s *fakeStore) Attempts(_ context.Context, id domain.JobID) ([]domain.JobAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.JobAttempt
	for _, a := range s.attempts {
		if a.JobID == id {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *fakeStore) AppendLog(_ context.Context, entry domain.JobLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, entry)
	return nil
}

func (s *fakeStore) Logs(_ context.Context, id domain.JobID, _ int) ([]domain.JobLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.JobLog
	for _, l := range s.logs {
		if l.JobID == id {
			out = append(out, l)
		}
	}
	return out, nil
}

var _ contracts.JobStore = (*fakeStore)(nil)

// newRunner builds a runner over a fresh store with an exact backoff, so a
// retry's scheduled time is an assertion rather than a range.
func newRunner(t *testing.T, store *fakeStore, clock *fakeClock) *jobs.Runner {
	t.Helper()
	return jobs.NewRunner(jobs.RunnerDeps{
		Store:   store,
		Clock:   clock,
		IDs:     &fakeIDs{},
		Owner:   "runner-a",
		Lease:   time.Minute,
		Batch:   4,
		Backoff: domain.Backoff{Base: 10 * time.Second, Max: time.Minute, Factor: 2},
	})
}

func enqueue(t *testing.T, store *fakeStore, kind string, maxAttempts int) domain.JobID {
	t.Helper()
	id := domain.JobID("job-" + kind)
	if _, _, err := store.Enqueue(context.Background(), domain.Job{
		ID: id, Kind: kind, Status: domain.JobPending,
		MaxAttempts: maxAttempts, CreatedAt: testNow,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

func TestRunnerRunsAClaimedJobAndMarksItSucceeded(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}
	runner := newRunner(t, store, clock)

	ran := 0
	runner.Register("work", func(context.Context, domain.Job) error { ran++; return nil })
	id := enqueue(t, store, "work", 3)

	if n, err := runner.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1, nil", n, err)
	}
	if ran != 1 {
		t.Fatalf("handler ran %d times, want 1", ran)
	}
	job, _ := store.FindByID(context.Background(), id)
	if job.Status != domain.JobSucceeded {
		t.Fatalf("status = %s, want %s", job.Status, domain.JobSucceeded)
	}

	// A succeeded job is not claimable again, which is what stops a completed
	// sweep from running on every poll for the rest of the process's life.
	if n, _ := runner.RunOnce(context.Background()); n != 0 {
		t.Fatalf("a succeeded job was claimed again (%d)", n)
	}
}

// The exit criterion in two halves: a failure is rescheduled rather than lost,
// and the delay grows.
func TestRunnerRetriesWithBackoffThenDeadLetters(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}
	runner := newRunner(t, store, clock)

	boom := errors.New("upstream said no")
	attempts := 0
	runner.Register("flaky", func(context.Context, domain.Job) error { attempts++; return boom })
	id := enqueue(t, store, "flaky", 3)

	// Attempt 1 → pending, runnable in 10s.
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job, _ := store.FindByID(context.Background(), id)
	if job.Status != domain.JobPending {
		t.Fatalf("after one failure status = %s, want %s", job.Status, domain.JobPending)
	}
	if want := testNow.Add(10 * time.Second); !job.ScheduledAt.Equal(want) {
		t.Fatalf("first retry scheduled %s, want %s", job.ScheduledAt, want)
	}

	// Not claimable until the backoff has elapsed — the property that makes it
	// a backoff rather than a counter.
	if n, _ := runner.RunOnce(context.Background()); n != 0 {
		t.Fatalf("a job inside its backoff was claimed (%d)", n)
	}

	// Attempt 2 → pending, runnable in 20s: the delay doubled.
	clock.advance(10 * time.Second)
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job, _ = store.FindByID(context.Background(), id)
	if want := testNow.Add(10 * time.Second).Add(20 * time.Second); !job.ScheduledAt.Equal(want) {
		t.Fatalf("second retry scheduled %s, want %s", job.ScheduledAt, want)
	}

	// Attempt 3 is the last allowed, so it dead-letters rather than retrying.
	clock.advance(20 * time.Second)
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job, _ = store.FindByID(context.Background(), id)
	if job.Status != domain.JobDead {
		t.Fatalf("status = %s, want %s", job.Status, domain.JobDead)
	}
	if job.LastError != boom.Error() {
		t.Fatalf("last error = %q, want %q", job.LastError, boom.Error())
	}
	if attempts != 3 {
		t.Fatalf("handler ran %d times, want 3", attempts)
	}

	// The row stays and carries its history: a dead-letter that deleted itself
	// would report as an empty queue.
	history, _ := store.Attempts(context.Background(), id)
	if len(history) != 3 {
		t.Fatalf("recorded %d attempts, want 3", len(history))
	}
	for _, a := range history {
		if a.Status != domain.JobAttemptFailed {
			t.Fatalf("attempt %d recorded as %s, want %s", a.Attempt, a.Status, domain.JobAttemptFailed)
		}
	}
	// And it says why, beside the job rather than only in telemetry.
	lines, _ := store.Logs(context.Background(), id, 0)
	if len(lines) != 3 {
		t.Fatalf("wrote %d job log lines, want 3", len(lines))
	}
	if lines[len(lines)-1].Level != "error" {
		t.Fatalf("the dead-letter line is level %q, want error", lines[len(lines)-1].Level)
	}
}

// A kind nothing handles cannot be fixed by retrying, and leaving it pending
// would have it claimed on every poll forever.
func TestRunnerDeadLettersAnUnhandledKindImmediately(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}
	runner := newRunner(t, store, clock)
	id := enqueue(t, store, "nobody.handles.this", 5)

	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job, _ := store.FindByID(context.Background(), id)
	if job.Status != domain.JobDead {
		t.Fatalf("status = %s, want %s", job.Status, domain.JobDead)
	}
}

// A panic in a handler must cost the job and not the process: the poll loop is
// a goroutine, and a panic there would take every request handler with it.
func TestRunnerSurvivesAPanickingHandler(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}
	runner := newRunner(t, store, clock)
	runner.Register("explodes", func(context.Context, domain.Job) error { panic("nope") })
	id := enqueue(t, store, "explodes", 2)

	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job, _ := store.FindByID(context.Background(), id)
	if job.Status != domain.JobPending {
		t.Fatalf("status = %s, want the job to be retried", job.Status)
	}
	if job.LastError == "" {
		t.Fatal("a panicking handler recorded no cause")
	}
}

// A claim failure is PostgreSQL being unreachable. The loop must survive it, or
// background work stops permanently because the database restarted once.
func TestRunnerReportsAClaimFailureWithoutCrashing(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}
	store.claimErr = contracts.NewError(contracts.Unavailable, "database is away")
	runner := newRunner(t, store, clock)

	n, err := runner.RunOnce(context.Background())
	if n != 0 || err == nil {
		t.Fatalf("RunOnce = %d, %v; want 0 and an error", n, err)
	}
	store.claimErr = nil
	runner.Register("work", func(context.Context, domain.Job) error { return nil })
	enqueue(t, store, "work", 3)
	if n, err := runner.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("the runner did not recover: RunOnce = %d, %v", n, err)
	}
}

// Survives a restart. A job left running by a process that died is not
// stranded: its lease lapses and the next runner takes it.
//
// Reclaiming turns on the lease having lapsed and nothing else — `Claim` never
// compares `leased_by` to the claiming owner. That is worth stating because
// the owner *is* the boot id, and under the Supervisor a restarted Platform
// adopts the same boot id its predecessor had (ADR 0060), so a new runner is
// no longer necessarily a new owner. This test uses two owners because that is
// the harder case to get right, not because the code depends on them differing.
func TestAJobAbandonedByADeadRunnerIsReclaimed(t *testing.T) {
	store, clock := newFakeStore(), &fakeClock{now: testNow}

	// The first runner claims and then "dies": nothing records an outcome.
	first := jobs.NewRunner(jobs.RunnerDeps{
		Store: store, Clock: clock, IDs: &fakeIDs{}, Owner: "boot-1", Lease: time.Minute,
	})
	id := enqueue(t, store, "work", 5)
	claimed, err := store.Claim(context.Background(), clock.Now(), "boot-1", time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}
	_ = first

	// Inside the lease, nobody else may take it.
	clock.advance(30 * time.Second)
	second := jobs.NewRunner(jobs.RunnerDeps{
		Store: store, Clock: clock, IDs: &fakeIDs{}, Owner: "boot-2", Lease: time.Minute,
	})
	ran := 0
	second.Register("work", func(context.Context, domain.Job) error { ran++; return nil })
	if n, _ := second.RunOnce(context.Background()); n != 0 {
		t.Fatalf("a live lease was stolen (%d)", n)
	}

	// Past it, the next boot picks it up.
	clock.advance(31 * time.Second)
	if n, err := second.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("an expired lease was not reclaimed: %d, %v", n, err)
	}
	if ran != 1 {
		t.Fatalf("handler ran %d times, want 1", ran)
	}
	job, _ := store.FindByID(context.Background(), id)
	if job.Status != domain.JobSucceeded {
		t.Fatalf("status = %s, want %s", job.Status, domain.JobSucceeded)
	}
	if job.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2 — the abandoned claim must still cost one", job.Attempt)
	}
}
