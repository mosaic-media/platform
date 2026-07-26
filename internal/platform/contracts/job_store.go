// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// JobStore is the queue of background work (ADR 0017's no-user case).
//
// It is deliberately **not on Tx**, and the reason is the opposite of the
// telemetry store's. Telemetry is off the transaction because it must never
// fail a request; this is off it because a claim *is* a transaction — the
// `SELECT … FOR UPDATE SKIP LOCKED` that hands one runner a job and lets
// another walk past it holds row locks for exactly as long as the claim, and
// enclosing it in a caller's unit of work would hold those locks for the length
// of somebody else's command.
//
// The consequence is stated rather than hidden: a job enqueued from inside a
// command does not commit with it. Nothing enqueues from a command today —
// every producer is a schedule — and when one does, the honest answer is an
// Enqueue reachable through Tx beside this one, not an Enqueue that pretends.
type JobStore interface {
	// Enqueue inserts a job, reporting whether the row was created. A job
	// carrying a ScheduleKey that is already queued is *not* an error: the
	// second caller gets created=false and the existing row, which is what
	// makes "enqueue this hour's sweep" safe to call on every tick, from every
	// process, and at every boot.
	Enqueue(ctx context.Context, job domain.Job) (stored domain.Job, created bool, err error)

	// Claim leases up to limit runnable jobs to owner until now+lease, and
	// spends an attempt on each.
	//
	// Runnable is two things: pending work whose ScheduledAt has arrived, and
	// running work whose lease has expired — the second being how a job
	// abandoned by a process that died gets picked up rather than stranded.
	Claim(ctx context.Context, now time.Time, owner string, lease time.Duration, limit int) ([]domain.Job, error)

	// Complete marks a claimed job succeeded.
	Complete(ctx context.Context, id domain.JobID, at time.Time) error

	// Retry returns a claimed job to pending, runnable no earlier than at.
	Retry(ctx context.Context, id domain.JobID, at time.Time, cause string) error

	// DeadLetter marks a claimed job as having exhausted its attempts. The row
	// stays: a dead-letter that deletes itself is a failure reported as an
	// empty queue.
	DeadLetter(ctx context.Context, id domain.JobID, at time.Time, cause string) error

	// FindByID reads one job, NotFound when there is none.
	FindByID(ctx context.Context, id domain.JobID) (domain.Job, error)

	// List reads jobs matching filter, most recently created first.
	List(ctx context.Context, filter domain.JobFilter) ([]domain.Job, error)

	// StartAttempt records that an attempt began, returning it with its id.
	StartAttempt(ctx context.Context, attempt domain.JobAttempt) (domain.JobAttempt, error)
	// FinishAttempt records how an attempt ended.
	FinishAttempt(ctx context.Context, attempt domain.JobAttempt) error
	// Attempts reads one job's attempt history, oldest first.
	Attempts(ctx context.Context, id domain.JobID) ([]domain.JobAttempt, error)

	// AppendLog stores one line a job recorded about itself.
	AppendLog(ctx context.Context, entry domain.JobLog) error
	// Logs reads one job's lines, oldest first.
	Logs(ctx context.Context, id domain.JobID, limit int) ([]domain.JobLog, error)
}
