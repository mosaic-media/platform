// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// jobStore is the PostgreSQL contracts.JobStore.
//
// Pool-backed rather than transaction-scoped, per the contract: a claim is its
// own transaction and must not be held inside anybody else's.
type jobStore struct {
	pool *pgxpool.Pool
}

// NewJobStore builds a pool-backed JobStore.
func NewJobStore(pool *pgxpool.Pool) contracts.JobStore {
	return &jobStore{pool: pool}
}

// defaultJobListLimit caps an unbounded listing. The jobs screen shows a
// window, and a queue that has run for a month should not be read whole to
// draw the top of it.
const defaultJobListLimit = 200

const jobColumns = `id, kind, payload, status, attempt, max_attempts, created_at,
	scheduled_at, leased_by, leased_until, last_error, updated_at, finished_at, schedule_key`

func (s *jobStore) Enqueue(ctx context.Context, job domain.Job) (domain.Job, bool, error) {
	if job.Status == "" {
		job.Status = domain.JobPending
	}
	if job.Payload == nil {
		// A nil []byte is SQL NULL to the driver and the column is NOT NULL.
		// Most jobs carry no payload — the kind is the whole instruction — so
		// this is the ordinary case rather than an edge one.
		job.Payload = []byte{}
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO jobs
		   (id, kind, payload, status, attempt, max_attempts, created_at,
		    scheduled_at, leased_by, leased_until, last_error, updated_at, finished_at, schedule_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '', NULL, '', $7, NULL, $9)
		 ON CONFLICT (schedule_key) WHERE schedule_key IS NOT NULL DO NOTHING
		 RETURNING `+jobColumns,
		string(job.ID), job.Kind, job.Payload, string(job.Status),
		job.Attempt, job.MaxAttempts, job.CreatedAt.UTC(),
		nullTime(job.ScheduledAt), nullString(job.ScheduleKey),
	)
	stored, err := scanJob(row)
	if err == nil {
		return stored, true, nil
	}
	if !isNoRows(err) {
		return domain.Job{}, false, mapError("enqueue job", err)
	}

	// DO NOTHING returns no row, which is the ordinary outcome for a recurring
	// schedule enqueuing an occurrence that is already queued. Read back what
	// is there so the caller gets the real job rather than the one it offered.
	existing, findErr := s.findByScheduleKey(ctx, job.ScheduleKey)
	if findErr != nil {
		return domain.Job{}, false, findErr
	}
	return existing, false, nil
}

func (s *jobStore) findByScheduleKey(ctx context.Context, key string) (domain.Job, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE schedule_key = $1`, key)
	job, err := scanJob(row)
	if err != nil {
		if isNoRows(err) {
			return domain.Job{}, contracts.NewError(contracts.NotFound, "job not found")
		}
		return domain.Job{}, mapError("find job by schedule key", err)
	}
	return job, nil
}

// Claim is the whole coordination mechanism, and it is one statement.
//
// `FOR UPDATE SKIP LOCKED` is what lets two runners poll the same queue without
// a lock table, a heartbeat protocol or a leader: each takes rows the other has
// not, and neither waits. Skipping rather than blocking is the load-bearing
// half — `FOR UPDATE` alone would make the second runner queue behind the
// first and turn a pool of workers into one worker with spectators.
//
// The UPDATE is in the same statement as the SELECT, so a claim is atomic
// without an explicit transaction: there is no window in which a row is locked
// but not yet marked running.
func (s *jobStore) Claim(ctx context.Context, now time.Time, owner string, lease time.Duration, limit int) ([]domain.Job, error) {
	if limit <= 0 {
		limit = 1
	}
	at := now.UTC()
	rows, err := s.pool.Query(ctx,
		`WITH claimable AS (
		     SELECT id FROM jobs
		      WHERE (status = 'pending' AND (scheduled_at IS NULL OR scheduled_at <= $1))
		         OR (status = 'running' AND leased_until IS NOT NULL AND leased_until <= $1)
		      ORDER BY scheduled_at NULLS FIRST, created_at
		      LIMIT $4
		      FOR UPDATE SKIP LOCKED
		 )
		 UPDATE jobs j
		    SET status       = 'running',
		        attempt      = j.attempt + 1,
		        leased_by    = $2,
		        leased_until = $3,
		        updated_at   = $1
		   FROM claimable c
		  WHERE j.id = c.id
		 RETURNING j.id, j.kind, j.payload, j.status, j.attempt, j.max_attempts, j.created_at,
		           j.scheduled_at, j.leased_by, j.leased_until, j.last_error, j.updated_at,
		           j.finished_at, j.schedule_key`,
		at, owner, at.Add(lease), limit,
	)
	if err != nil {
		return nil, mapError("claim jobs", err)
	}
	defer rows.Close()

	var claimed []domain.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, mapError("scan claimed job", err)
		}
		claimed = append(claimed, job)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("claim jobs", err)
	}
	return claimed, nil
}

func (s *jobStore) Complete(ctx context.Context, id domain.JobID, at time.Time) error {
	return s.finish(ctx, id, domain.JobSucceeded, at, "", nil)
}

func (s *jobStore) Retry(ctx context.Context, id domain.JobID, at time.Time, cause string) error {
	runAt := at.UTC()
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs
		    SET status = 'pending', scheduled_at = $2, last_error = $3,
		        leased_by = '', leased_until = NULL, updated_at = $2
		  WHERE id = $1`,
		string(id), runAt, cause)
	if err != nil {
		return mapError("retry job", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "job not found")
	}
	return nil
}

func (s *jobStore) DeadLetter(ctx context.Context, id domain.JobID, at time.Time, cause string) error {
	finished := at.UTC()
	return s.finish(ctx, id, domain.JobDead, at, cause, &finished)
}

func (s *jobStore) finish(ctx context.Context, id domain.JobID, status domain.JobStatus, at time.Time, cause string, finished *time.Time) error {
	when := at.UTC()
	if finished == nil {
		finished = &when
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs
		    SET status = $2, last_error = $3, finished_at = $4,
		        leased_by = '', leased_until = NULL, updated_at = $5
		  WHERE id = $1`,
		string(id), string(status), cause, *finished, when)
	if err != nil {
		return mapError("finish job", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "job not found")
	}
	return nil
}

func (s *jobStore) FindByID(ctx context.Context, id domain.JobID) (domain.Job, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1`, string(id))
	job, err := scanJob(row)
	if err != nil {
		if isNoRows(err) {
			return domain.Job{}, contracts.NewError(contracts.NotFound, "job not found")
		}
		return domain.Job{}, mapError("find job by id", err)
	}
	return job, nil
}

func (s *jobStore) List(ctx context.Context, filter domain.JobFilter) ([]domain.Job, error) {
	limit := filter.Limit
	if limit <= 0 || limit > defaultJobListLimit {
		limit = defaultJobListLimit
	}
	// Both filters are passed as values with an "unset means every" guard in
	// SQL rather than as an assembled WHERE clause: the query plan is the same
	// for every caller, and there is no string concatenation anywhere near a
	// value.
	rows, err := s.pool.Query(ctx,
		`SELECT `+jobColumns+` FROM jobs
		  WHERE ($1 = '' OR kind = $1)
		    AND ($2 = '' OR status = $2)
		  ORDER BY created_at DESC, id DESC
		  LIMIT $3`,
		filter.Kind, string(filter.Status), limit)
	if err != nil {
		return nil, mapError("list jobs", err)
	}
	defer rows.Close()

	var out []domain.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, mapError("scan job", err)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list jobs", err)
	}
	return out, nil
}

func (s *jobStore) StartAttempt(ctx context.Context, attempt domain.JobAttempt) (domain.JobAttempt, error) {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO job_attempts (id, job_id, attempt, started_at, finished_at, status, error, duration_us, runner)
		 VALUES ($1, $2, $3, $4, NULL, $5, '', 0, $6)
		 ON CONFLICT (job_id, attempt) DO UPDATE
		    SET started_at = EXCLUDED.started_at, status = EXCLUDED.status,
		        error = '', duration_us = 0, runner = EXCLUDED.runner, finished_at = NULL`,
		string(attempt.ID), string(attempt.JobID), attempt.Attempt,
		attempt.StartedAt.UTC(), string(domain.JobAttemptRunning), attempt.Runner)
	if err != nil {
		return domain.JobAttempt{}, mapError("start job attempt", err)
	}
	attempt.Status = domain.JobAttemptRunning
	return attempt, nil
}

func (s *jobStore) FinishAttempt(ctx context.Context, attempt domain.JobAttempt) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE job_attempts
		    SET finished_at = $3, status = $4, error = $5, duration_us = $6
		  WHERE job_id = $1 AND attempt = $2`,
		string(attempt.JobID), attempt.Attempt, attempt.FinishedAt.UTC(),
		string(attempt.Status), attempt.Error, attempt.Duration.Microseconds())
	if err != nil {
		return mapError("finish job attempt", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "job attempt not found")
	}
	return nil
}

func (s *jobStore) Attempts(ctx context.Context, id domain.JobID) ([]domain.JobAttempt, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, job_id, attempt, started_at, finished_at, status, error, duration_us, runner
		   FROM job_attempts WHERE job_id = $1 ORDER BY attempt`, string(id))
	if err != nil {
		return nil, mapError("list job attempts", err)
	}
	defer rows.Close()

	var out []domain.JobAttempt
	for rows.Next() {
		var (
			a          domain.JobAttempt
			attemptID  string
			jobID      string
			finishedAt *time.Time
			status     string
			durationUS int64
		)
		if err := rows.Scan(&attemptID, &jobID, &a.Attempt, &a.StartedAt, &finishedAt,
			&status, &a.Error, &durationUS, &a.Runner); err != nil {
			return nil, mapError("scan job attempt", err)
		}
		a.ID = domain.JobAttemptID(attemptID)
		a.JobID = domain.JobID(jobID)
		a.Status = domain.JobAttemptStatus(status)
		a.Duration = time.Duration(durationUS) * time.Microsecond
		if finishedAt != nil {
			a.FinishedAt = *finishedAt
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list job attempts", err)
	}
	return out, nil
}

func (s *jobStore) AppendLog(ctx context.Context, entry domain.JobLog) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO job_logs (id, job_id, logged_at, level, message, trace) VALUES ($1, $2, $3, $4, $5, $6)`,
		string(entry.ID), string(entry.JobID), entry.LoggedAt.UTC(), entry.Level, entry.Message, entry.Trace)
	if err != nil {
		return mapError("append job log", err)
	}
	return nil
}

func (s *jobStore) Logs(ctx context.Context, id domain.JobID, limit int) ([]domain.JobLog, error) {
	if limit <= 0 || limit > defaultJobListLimit {
		limit = defaultJobListLimit
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, job_id, logged_at, level, message, trace FROM job_logs
		  WHERE job_id = $1 ORDER BY logged_at, id LIMIT $2`, string(id), limit)
	if err != nil {
		return nil, mapError("list job logs", err)
	}
	defer rows.Close()

	var out []domain.JobLog
	for rows.Next() {
		var (
			entry domain.JobLog
			logID string
			jobID string
		)
		if err := rows.Scan(&logID, &jobID, &entry.LoggedAt, &entry.Level, &entry.Message, &entry.Trace); err != nil {
			return nil, mapError("scan job log", err)
		}
		entry.ID = domain.JobLogID(logID)
		entry.JobID = domain.JobID(jobID)
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list job logs", err)
	}
	return out, nil
}

func scanJob(row pgx.Row) (domain.Job, error) {
	var (
		job         domain.Job
		id          string
		status      string
		scheduledAt *time.Time
		leasedUntil *time.Time
		updatedAt   *time.Time
		finishedAt  *time.Time
		scheduleKey *string
	)
	if err := row.Scan(&id, &job.Kind, &job.Payload, &status, &job.Attempt, &job.MaxAttempts,
		&job.CreatedAt, &scheduledAt, &job.LeasedBy, &leasedUntil, &job.LastError,
		&updatedAt, &finishedAt, &scheduleKey); err != nil {
		return domain.Job{}, err
	}
	job.ID = domain.JobID(id)
	job.Status = domain.JobStatus(status)
	job.ScheduledAt = derefTime(scheduledAt)
	job.LeasedUntil = derefTime(leasedUntil)
	job.UpdatedAt = derefTime(updatedAt)
	job.FinishedAt = derefTime(finishedAt)
	if scheduleKey != nil {
		job.ScheduleKey = *scheduleKey
	}
	return job, nil
}

// nullTime maps a zero time onto SQL NULL, so "not scheduled" and "scheduled
// for the zero instant" are not the same row.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// nullString maps an empty string onto SQL NULL. It matters for schedule_key:
// the unique index is over non-null values, so an empty string would make every
// hand-enqueued job collide with every other.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
