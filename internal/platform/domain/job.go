// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import (
	"math"
	"time"
)

// JobID identifies a queued unit of background work.
type JobID ID

// JobAttemptID identifies one try at running a Job.
type JobAttemptID ID

// JobLogID identifies one line a Job recorded about itself.
type JobLogID ID

// JobStatus is where a Job is in its life. It is a closed set: the Platform
// branches on every value, and the column is the queue's whole coordination
// mechanism, so an unrecognised status would be work nothing ever claims.
type JobStatus string

const (
	// JobPending is queued and not yet claimed. A retry returns here with
	// ScheduledAt moved forward, which is why a retry is not its own status:
	// "waiting to run" is one state whether or not it has run before.
	JobPending JobStatus = "pending"
	// JobRunning is claimed by a runner and leased until LeasedUntil. A job
	// left here by a process that died is reclaimed once that lease expires —
	// which is what makes the queue survive a restart rather than stranding
	// whatever was in flight.
	JobRunning JobStatus = "running"
	// JobSucceeded finished without an error and will not run again.
	JobSucceeded JobStatus = "succeeded"
	// JobDead exhausted its attempts. It is kept rather than deleted: a
	// dead-letter nobody can read is a failure that reports as an empty queue,
	// and the whole point of exhausting attempts is that a human has to look.
	JobDead JobStatus = "dead"
)

// Job is one unit of background work.
//
// It carries no principal. Background work has no session to forward
// (ADR 0017), so who a job acts as is decided by the Platform that runs it and
// is not something the row can assert — a payload naming a user would be a
// privilege escalation written to a queue.
type Job struct {
	ID      JobID
	Kind    string
	Payload []byte
	Status  JobStatus
	// Attempt is how many times a runner has claimed this job. It is
	// incremented by the claim rather than by the outcome, so a runner that
	// dies mid-handler still spends an attempt — otherwise a job that
	// reliably kills its process would be retried forever.
	Attempt     int
	MaxAttempts int
	CreatedAt   time.Time
	// ScheduledAt is the earliest time this job may be claimed. Zero means
	// immediately. A retry moves it forward by the backoff.
	ScheduledAt time.Time
	// LeasedBy names the runner holding the claim, and LeasedUntil is when
	// that claim lapses. Both are only meaningful while Status is JobRunning.
	LeasedBy    string
	LeasedUntil time.Time
	LastError   string
	UpdatedAt   time.Time
	FinishedAt  time.Time
	// ScheduleKey is the idempotency key a recurring schedule enqueues under —
	// one per (kind, occurrence). Empty for a job enqueued by hand.
	ScheduleKey string
}

// Terminal reports whether the job has stopped moving of its own accord.
func (j Job) Terminal() bool {
	return j.Status == JobSucceeded || j.Status == JobDead
}

// Exhausted reports whether the attempt just spent was the last one allowed.
func (j Job) Exhausted() bool {
	return j.MaxAttempts > 0 && j.Attempt >= j.MaxAttempts
}

// JobAttempt records one try: when it started, how long it ran, and how it
// ended. The history is kept because the interesting failure is usually the
// shape of the sequence — three timeouts then a success is a different problem
// from three different errors.
type JobAttempt struct {
	ID         JobAttemptID
	JobID      JobID
	Attempt    int
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
	Status     JobAttemptStatus
	Error      string
	Runner     string
}

// JobAttemptStatus is how one attempt ended.
type JobAttemptStatus string

const (
	// JobAttemptRunning is an attempt in flight, or one whose runner died
	// before it could record an outcome.
	JobAttemptRunning JobAttemptStatus = "running"
	// JobAttemptSucceeded is an attempt whose handler returned no error.
	JobAttemptSucceeded JobAttemptStatus = "succeeded"
	// JobAttemptFailed is an attempt whose handler returned an error.
	JobAttemptFailed JobAttemptStatus = "failed"
)

// JobLog is one line a job recorded about itself, stored beside the job rather
// than only in telemetry.
//
// Both, deliberately. Telemetry is where the trace lives and it is subject to
// retention (ADR 0058), so a job that failed a fortnight ago would have lost
// the record of why by the time anyone asked. These rows outlive that window
// and are read from the job itself, which is where somebody looking at a
// dead-letter starts.
type JobLog struct {
	ID       JobLogID
	JobID    JobID
	LoggedAt time.Time
	Level    string
	Message  string
	// Trace is the trace the line was written inside — a job run mints its own
	// root trace, because it is nobody's request. It is what joins a
	// dead-lettered job to the waterfall of the attempt that killed it, for as
	// long as telemetry retention still holds that trace. Empty for a line
	// written outside one.
	Trace string
}

// JobFilter narrows a job listing.
type JobFilter struct {
	// Kind restricts to one job kind; empty means every kind.
	Kind string
	// Status restricts to one status; empty means every status.
	Status JobStatus
	// Limit caps the rows returned. Zero takes the store's default.
	Limit int
}

// Backoff is the retry delay schedule: exponential from Base, doubling per
// attempt (or by Factor), capped at Max.
//
// Capped rather than unbounded, because the failure this protects against is
// an upstream that is down: doubling forever turns a service that comes back
// after a day into a job that next tries in a fortnight. The cap is what makes
// "retries with backoff" and "recovers on its own" the same behaviour.
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Factor float64
	// Jitter spreads the delay by up to this fraction either side, so a batch
	// of jobs that failed together does not retry in lockstep. Zero is exact,
	// which is what a test wants.
	Jitter float64
	// rand supplies the jitter draw. Nil means no jitter regardless of the
	// fraction, so a zero-value Backoff is deterministic.
	rand func() float64
}

// DefaultBackoff is the schedule a runner uses unless told otherwise: a
// half-minute, doubling to a ceiling of ten, spread by a tenth.
func DefaultBackoff(random func() float64) Backoff {
	return Backoff{
		Base:   30 * time.Second,
		Max:    10 * time.Minute,
		Factor: 2,
		Jitter: 0.1,
		rand:   random,
	}
}

// Delay is how long to wait before attempt n runs. n is 1-based: the delay
// after the first failure is Base.
func (b Backoff) Delay(n int) time.Duration {
	if b.Base <= 0 {
		return 0
	}
	factor := b.Factor
	if factor < 1 {
		factor = 2
	}
	if n < 1 {
		n = 1
	}
	// Computed in float and clamped before conversion. An attempt counter that
	// has run away — a job stuck in a claim loop — would otherwise overflow
	// int64 and produce a negative delay, which is a job that retries instantly
	// forever.
	d := float64(b.Base) * math.Pow(factor, float64(n-1))
	if b.Max > 0 && d > float64(b.Max) {
		d = float64(b.Max)
	}
	if b.Jitter > 0 && b.rand != nil {
		d *= 1 + b.Jitter*(2*b.rand()-1)
	}
	if d < 0 {
		return 0
	}
	if d > float64(math.MaxInt64) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(d)
}
