// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionJobRead is reading the background-work queue.
//
// Its own action rather than a reuse of telemetry.read, even though the screen
// that shows it lives behind the same expert-mode switch. They reveal different
// things: telemetry is what every user did, and the queue is what the install
// did to itself. An operator who should see a stuck sweep and a dead-lettered
// import need not also be able to read everyone's search history.
//
// It sits in the superuser preset beside the telemetry actions, and an
// administrator is granted it individually.
const ActionJobRead policy.Action = "job.read"

// ListJobsQuery reads the background-work queue.
type ListJobsQuery struct {
	Caller v1.Caller
	Filter domain.JobFilter
}

// ListJobsResult is the matching jobs, most recently created first.
type ListJobsResult struct {
	Jobs []domain.Job
}

// ListJobs returns the queue: what has run, what is running, what is waiting to
// be retried and what has been dead-lettered.
//
// A dead-letter is the reason this query exists: a queue that gives up on a job
// and has no surface looks exactly like a queue with nothing to do.
func (s *Service) ListJobs(ctx context.Context, q ListJobsQuery) (ListJobsResult, error) {
	if _, err := s.enter(ctx, q.Caller, ActionJobRead, policy.Resource{Type: "job"}); err != nil {
		return ListJobsResult{}, err
	}
	if s.jobs == nil {
		return ListJobsResult{}, contracts.NewError(contracts.Unavailable, "this Platform has no jobs queue")
	}
	jobs, err := s.jobs.List(ctx, q.Filter)
	if err != nil {
		return ListJobsResult{}, err
	}
	return ListJobsResult{Jobs: jobs}, nil
}

// GetJobQuery reads one job with its attempt history and its own log lines.
type GetJobQuery struct {
	Caller v1.Caller
	JobID  domain.JobID
}

// GetJobResult is one job and everything recorded about it.
type GetJobResult struct {
	Job      domain.Job
	Attempts []domain.JobAttempt
	Logs     []domain.JobLog
}

// GetJob returns one job, the attempts made at it, and what it said about
// itself.
//
// The three together are what makes a failure legible: the job says what state
// it ended in, the attempts say whether the failures were one thing repeated or
// several different ones, and the lines say what the handler was doing. The
// lines are stored beside the job rather than read back out of telemetry
// precisely so this still answers after retention has dropped the trace
// (platform#36).
func (s *Service) GetJob(ctx context.Context, q GetJobQuery) (GetJobResult, error) {
	if q.JobID == "" {
		return GetJobResult{}, contracts.NewError(contracts.InvalidArgument, "job id is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionJobRead, policy.Resource{Type: "job", ID: string(q.JobID)}); err != nil {
		return GetJobResult{}, err
	}
	if s.jobs == nil {
		return GetJobResult{}, contracts.NewError(contracts.Unavailable, "this Platform has no jobs queue")
	}

	job, err := s.jobs.FindByID(ctx, q.JobID)
	if err != nil {
		return GetJobResult{}, err
	}
	attempts, err := s.jobs.Attempts(ctx, q.JobID)
	if err != nil {
		return GetJobResult{}, err
	}
	logs, err := s.jobs.Logs(ctx, q.JobID, 0)
	if err != nil {
		return GetJobResult{}, err
	}
	return GetJobResult{Job: job, Attempts: attempts, Logs: logs}, nil
}
