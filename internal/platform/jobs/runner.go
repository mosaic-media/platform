// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// Handler runs one job. It is given the job so a kind can carry a payload, and
// it returns an error to be retried or — once the attempts are spent —
// dead-lettered.
//
// A handler must be **idempotent**. At-least-once is the only delivery a
// claim-and-lease queue can offer: a process that dies between finishing the
// work and recording the outcome leaves a job that will be reclaimed and run
// again, and no amount of care inside the runner removes that window.
type Handler func(ctx context.Context, job domain.Job) error

// Defaults for a Runner built with the zero value of the corresponding field.
const (
	// DefaultPollInterval is how often an idle runner asks for work. Short
	// enough that a job enqueued by hand feels immediate, long enough that an
	// idle Platform is not running a query a second.
	DefaultPollInterval = 2 * time.Second
	// DefaultLease is how long a claim is held before another runner may take
	// the job. It must exceed DefaultHandlerTimeout, or a runner would lose its
	// own job to a peer while still working on it.
	DefaultLease = 10 * time.Minute
	// DefaultHandlerTimeout bounds one attempt. A handler that hangs must fail
	// and be retried rather than holding a lease until the process restarts.
	DefaultHandlerTimeout = 5 * time.Minute
	// DefaultBatch is how many jobs one claim takes.
	DefaultBatch = 4
	// DefaultMaxAttempts is how many tries a job gets before dead-lettering.
	DefaultMaxAttempts = 5
)

// RunnerDeps are what a Runner is built from.
type RunnerDeps struct {
	Store contracts.JobStore
	Clock contracts.Clock
	IDs   contracts.IDGenerator
	// Owner names this runner in a lease and in the attempt history. The boot
	// id is the right value: it identifies the process that held the claim, so
	// a job abandoned across a restart says which run left it.
	Owner string

	// The knobs, each defaulting when zero.
	PollInterval   time.Duration
	Lease          time.Duration
	HandlerTimeout time.Duration
	Batch          int
	Backoff        domain.Backoff
}

// Runner claims jobs and runs the handler registered for their kind.
type Runner struct {
	store          contracts.JobStore
	clock          contracts.Clock
	ids            contracts.IDGenerator
	owner          string
	poll           time.Duration
	lease          time.Duration
	handlerTimeout time.Duration
	batch          int
	backoff        domain.Backoff

	mu       sync.RWMutex
	handlers map[string]Handler

	// done is closed when the poll loop has exited, so a caller can wait for a
	// clean stop rather than racing the process into shutdown while a handler
	// is still writing.
	done chan struct{}
}

// NewRunner builds a Runner over deps, filling in the defaults.
func NewRunner(deps RunnerDeps) *Runner {
	r := &Runner{
		store:          deps.Store,
		clock:          deps.Clock,
		ids:            deps.IDs,
		owner:          deps.Owner,
		poll:           deps.PollInterval,
		lease:          deps.Lease,
		handlerTimeout: deps.HandlerTimeout,
		batch:          deps.Batch,
		backoff:        deps.Backoff,
		handlers:       map[string]Handler{},
		done:           make(chan struct{}),
	}
	if r.poll <= 0 {
		r.poll = DefaultPollInterval
	}
	if r.lease <= 0 {
		r.lease = DefaultLease
	}
	if r.handlerTimeout <= 0 {
		r.handlerTimeout = DefaultHandlerTimeout
	}
	if r.batch <= 0 {
		r.batch = DefaultBatch
	}
	if r.backoff.Base <= 0 {
		r.backoff = domain.DefaultBackoff(nil)
	}
	return r
}

// Register binds a handler to a job kind. Registering twice for one kind
// replaces the first, which only a composition mistake would do.
func (r *Runner) Register(kind string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[kind] = h
}

// Kinds is every registered kind, for the composition root to report at boot.
func (r *Runner) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	return out
}

func (r *Runner) handler(kind string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[kind]
	return h, ok
}

// Start runs the poll loop until ctx ends. It returns immediately.
func (r *Runner) Start(ctx context.Context) {
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(r.poll)
		defer ticker.Stop()
		for {
			// Drain before waiting, so a batch that filled the claim limit is
			// followed straight away by another rather than after a poll
			// interval. A queue that has just been given ten jobs should not
			// take twenty seconds to notice the last six.
			for {
				claimed, err := r.RunOnce(ctx)
				if err != nil || claimed < r.batch {
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Wait blocks until the poll loop started by Start has exited.
func (r *Runner) Wait(ctx context.Context) {
	select {
	case <-r.done:
	case <-ctx.Done():
	}
}

// RunOnce claims a batch and runs it, returning how many jobs it ran. It is
// exported because it is what a test drives: the poll loop adds only a ticker.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	claimed, err := r.store.Claim(ctx, r.clock.Now(), r.owner, r.lease, r.batch)
	if err != nil {
		// A claim failure is the database being unreachable, which the poll
		// loop must survive: the alternative is a Platform whose background
		// work stops permanently because PostgreSQL restarted once.
		telemetry.From(ctx).For("jobs").Error("claiming jobs failed", telemetry.Err(err))
		return 0, err
	}
	for _, job := range claimed {
		r.run(ctx, job)
	}
	return len(claimed), nil
}

// run executes one claimed job and records how it went.
//
// Each job gets its own root trace. It is not part of anybody's request — that
// is the entire point of a job — so continuing an inbound trace would be
// inventing a causal link, and leaving it traceless would put the one class of
// work nobody is watching outside the only tool for watching it.
func (r *Runner) run(parent context.Context, job domain.Job) {
	ctx := telemetry.TraceInto(parent, telemetry.NewRootTrace())
	tc, _ := telemetry.TraceFrom(ctx)
	lg := telemetry.From(ctx).For("jobs").WithTrace(tc).With(
		telemetry.String("kind", job.Kind),
		telemetry.Identifier("job", string(job.ID)))
	ctx = telemetry.Into(ctx, lg)

	ctx, span := telemetry.Start(ctx, "job "+job.Kind,
		telemetry.String("kind", job.Kind),
		telemetry.Int("attempt", job.Attempt))
	defer span.End()

	started := r.clock.Now()
	attempt := domain.JobAttempt{
		ID:        domain.JobAttemptID(r.ids.NewID()),
		JobID:     job.ID,
		Attempt:   job.Attempt,
		StartedAt: started,
		Runner:    r.owner,
	}
	if _, err := r.store.StartAttempt(ctx, attempt); err != nil {
		lg.Error("recording the job attempt failed", telemetry.Err(err))
	}

	handler, ok := r.handler(job.Kind)
	if !ok {
		// A job whose kind nothing handles is dead on arrival: retrying cannot
		// help, because the handler set is fixed for the life of the process.
		// It is dead-lettered rather than left pending so it stops being
		// claimed every poll — and so a human sees it, since an unhandled kind
		// is a composition mistake or a job left behind by a downgrade.
		err := fmt.Errorf("no handler registered for job kind %q", job.Kind)
		span.Fail(string(contracts.InvalidArgument), err)
		r.finishAttempt(ctx, attempt, started, err)
		r.bury(ctx, job, err)
		return
	}

	runCtx, cancel := context.WithTimeout(ctx, r.handlerTimeout)
	err := runHandler(runCtx, handler, job)
	cancel()

	r.finishAttempt(ctx, attempt, started, err)
	if err == nil {
		if cErr := r.store.Complete(ctx, job.ID, r.clock.Now()); cErr != nil {
			lg.Error("marking the job complete failed", telemetry.Err(cErr))
		}
		r.log(ctx, job.ID, "info", fmt.Sprintf("attempt %d succeeded in %s", job.Attempt, r.clock.Now().Sub(started).Round(time.Millisecond)))
		lg.Info("job succeeded", telemetry.Duration("took", r.clock.Now().Sub(started)))
		return
	}

	span.Fail(string(contracts.CategoryOf(err)), err)
	if job.Exhausted() {
		r.bury(ctx, job, err)
		return
	}

	delay := r.backoff.Delay(job.Attempt)
	at := r.clock.Now().Add(delay)
	if rErr := r.store.Retry(ctx, job.ID, at, err.Error()); rErr != nil {
		lg.Error("rescheduling the job failed", telemetry.Err(rErr))
	}
	r.log(ctx, job.ID, "warn", fmt.Sprintf("attempt %d failed: %s — retrying in %s", job.Attempt, err, delay.Round(time.Second)))
	lg.Warn("job failed; retrying",
		telemetry.Int("attempt", job.Attempt),
		telemetry.Int("max_attempts", job.MaxAttempts),
		telemetry.Duration("retry_in", delay),
		telemetry.Err(err))
}

// bury dead-letters a job whose attempts are spent.
func (r *Runner) bury(ctx context.Context, job domain.Job, cause error) {
	if err := r.store.DeadLetter(ctx, job.ID, r.clock.Now(), cause.Error()); err != nil {
		telemetry.From(ctx).Error("dead-lettering the job failed", telemetry.Err(err))
	}
	r.log(ctx, job.ID, "error", fmt.Sprintf("dead-lettered after %d %s: %s",
		job.Attempt, plural(job.Attempt, "attempt"), cause))
	// Error, not Warn. A dead-letter is the queue saying it has given up, and
	// it is the one job outcome that will not fix itself.
	telemetry.From(ctx).Error("job dead-lettered",
		telemetry.Int("attempts", job.Attempt),
		telemetry.Err(cause))
}

func (r *Runner) finishAttempt(ctx context.Context, attempt domain.JobAttempt, started time.Time, err error) {
	attempt.FinishedAt = r.clock.Now()
	attempt.Duration = attempt.FinishedAt.Sub(started)
	attempt.Status = domain.JobAttemptSucceeded
	if err != nil {
		attempt.Status = domain.JobAttemptFailed
		attempt.Error = err.Error()
	}
	if fErr := r.store.FinishAttempt(ctx, attempt); fErr != nil {
		telemetry.From(ctx).Error("recording the attempt outcome failed", telemetry.Err(fErr))
	}
}

// log writes one line beside the job, carrying the trace the run minted so the
// line joins to the waterfall that produced it. Best-effort: a job that ran is
// not undone because the note about it could not be stored.
func (r *Runner) log(ctx context.Context, id domain.JobID, level, message string) {
	tc, _ := telemetry.TraceFrom(ctx)
	err := r.store.AppendLog(ctx, domain.JobLog{
		ID:       domain.JobLogID(r.ids.NewID()),
		JobID:    id,
		LoggedAt: r.clock.Now(),
		Level:    level,
		Message:  message,
		Trace:    tc.TraceIDString(),
	})
	if err != nil {
		telemetry.From(ctx).Warn("appending the job log failed", telemetry.Err(err))
	}
}

// runHandler invokes a handler and converts a panic into an error.
//
// A handler is Platform code, so a panic is a bug rather than an expected
// outcome — but the poll loop is a goroutine, and a panic there takes the whole
// process down along with the request handlers that had nothing to do with it.
// Turning it into a failed attempt keeps the blast radius to the job.
func runHandler(ctx context.Context, h Handler, job domain.Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job handler panicked: %v", p)
		}
	}()
	if err := h(ctx, job); err != nil {
		return err
	}
	// A handler that returns nil having been cancelled did not finish. Treated
	// as a failure so the job is retried rather than recorded as done, which is
	// the difference between a shutdown mid-sweep and a sweep that swept.
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("job handler exceeded its time limit: %w", ctxErr)
	}
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
