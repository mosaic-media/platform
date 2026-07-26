// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package jobs is the composition wiring for background work: it names the job
// kinds this binary can run, binds each to the application command that
// performs it, and declares which of them recur.
//
// It sits beside the runner rather than inside it for the same reason
// registerCapabilities sits in the composition root: the runner is a mechanism
// and this is the list of what a particular build actually does. The runner
// therefore depends on contracts alone, and knowledge of the application
// services stays here.
//
// **Every handler runs as the system principal** (ADR 0017). A handler asks the
// Service for that caller and forwards it, exactly as a module forwards the
// caller it was handed — the boundary is the ordinary one, and there is no path
// here that reaches a store directly.
package jobs

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/jobs"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// The job kinds this binary carries.
//
// A kind is a stable string because it is written into a row that outlives the
// process. Renaming one strands whatever is queued under the old name — which
// the runner reports as a dead-letter naming the unhandled kind, rather than
// silently dropping it.
const (
	// KindTelemetryRetention extends the telemetry partition window and drops
	// the partitions retention has run out on (ADR 0058). It is the first
	// caller of the six the roadmap queued behind this runner, and it was
	// picked to prove the runner precisely because it was already configured
	// and already doing nothing durable: retention ran in a goroutine that only
	// existed while the process did, so a Platform down for a month came back
	// with a month of records it had intended to drop.
	KindTelemetryRetention = "telemetry.retention"
)

// TelemetryRetentionInterval is how often the retention sweep runs.
//
// Hourly, against a retention measured in days, which is deliberate
// over-provision: a partition boundary is daily, so the sweep only has to catch
// one midnight, and running twenty-four times a day means a missed hour, a
// restart or a failed attempt costs nothing at all.
const TelemetryRetentionInterval = time.Hour

// Deps are what the runner and scheduler are built from.
type Deps struct {
	Service *app.Service
	Store   contracts.JobStore
	Clock   contracts.Clock
	IDs     contracts.IDGenerator
	// Owner names this process in a lease and in the attempt history. The boot
	// id is what belongs here: it says which run of the Platform held a claim,
	// which is exactly the question asked of a job abandoned across a restart.
	Owner string
}

// Runtime is the pair a caller starts and stops together.
type Runtime struct {
	Runner    *jobs.Runner
	Scheduler *jobs.Scheduler
}

// New builds the runner with every handler this binary carries registered, and
// the scheduler over the recurring ones.
func New(deps Deps) Runtime {
	runner := jobs.NewRunner(jobs.RunnerDeps{
		Store: deps.Store,
		Clock: deps.Clock,
		IDs:   deps.IDs,
		Owner: deps.Owner,
	})
	runner.Register(KindTelemetryRetention, telemetryRetention(deps.Service))

	scheduler := jobs.NewScheduler(jobs.SchedulerDeps{
		Store: deps.Store,
		Clock: deps.Clock,
		IDs:   deps.IDs,
		Schedules: []jobs.Schedule{
			{Kind: KindTelemetryRetention, Every: TelemetryRetentionInterval},
		},
	})

	return Runtime{Runner: runner, Scheduler: scheduler}
}

// Start begins claiming and scheduling until ctx ends.
func (r Runtime) Start(ctx context.Context) {
	r.Runner.Start(ctx)
	r.Scheduler.Start(ctx)
}

// telemetryRetention is the handler for KindTelemetryRetention.
//
// It is idempotent, which the runner requires of every handler and this one
// gets for free: creating a partition that exists is a no-op, and dropping one
// that is already gone is too. A job reclaimed after a crash therefore repeats
// harmlessly.
func telemetryRetention(svc *app.Service) jobs.Handler {
	return func(ctx context.Context, _ domain.Job) error {
		res, err := svc.PurgeTelemetry(ctx, app.PurgeTelemetryCommand{Caller: svc.SystemCaller()})
		if err != nil {
			return err
		}
		telemetry.From(ctx).Info("telemetry retention swept",
			telemetry.Int("partitions_dropped", res.Dropped),
			telemetry.Duration("log_retention", res.Retention.Logs),
			telemetry.Duration("span_retention", res.Retention.Spans))
		return nil
	}
}
