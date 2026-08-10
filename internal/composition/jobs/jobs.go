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
// **Every handler runs as the system principal** (platform#13). A handler asks the
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
	// the partitions retention has run out on (platform#36). It is the first
	// caller of the six the roadmap queued behind this runner, and it was
	// picked to prove the runner precisely because it was already configured
	// and already doing nothing durable: retention ran in a goroutine that only
	// existed while the process did, so a Platform down for a month came back
	// with a month of records it had intended to drop.
	KindTelemetryRetention = "telemetry.retention"
	// KindSessionTokenSweep deletes expired access and refresh tokens
	// (platform#58). The access-token table is the fastest-growing thing in the
	// schema — one row per client per ten minutes, forever — and nothing else
	// would ever remove them.
	KindSessionTokenSweep = "session.tokens.sweep"
	// KindLibraryMaintenance evaluates the library rules and reconciles
	// (platform#60). It is the third of the six callers this runner was built for,
	// and the first that does work a person would otherwise have to do by hand
	// rather than housekeeping nobody would miss.
	KindLibraryMaintenance = "library.maintenance"
	// KindLibraryAvailability re-asks the metadata providers where the library's
	// works can be watched, oldest answer first (roadmap M2.5).
	//
	// **It is the reason the streaming-service facet may exist at all.** Both
	// halves of that grouping worked long before this: a provider answers with
	// availability and the store filters on it. The surface was withheld because
	// nothing refreshed a fact that churns monthly, and a group saying "on
	// Netflix" about a title that left in March is worse than an absent group —
	// a user can see a missing feature and cannot see a lying one.
	KindLibraryAvailability = "library.availability"
)

// TelemetryRetentionInterval is how often the retention sweep runs.
//
// Hourly, against a retention measured in days, which is deliberate
// over-provision: a partition boundary is daily, so the sweep only has to catch
// one midnight, and running twenty-four times a day means a missed hour, a
// restart or a failed attempt costs nothing at all.
const TelemetryRetentionInterval = time.Hour

// SessionTokenSweepInterval is how often expired credentials are deleted. Daily
// rather than hourly: nothing is wrong while an expired token sits in a table
// it can never be validated from, so this is disk hygiene and does not need to
// be prompt.
const SessionTokenSweepInterval = 24 * time.Hour

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
	// LibraryMaintenance is the configured schedule for the library pass
	// (platform#60). The composition root reads it from the Active configuration
	// and passes it in, rather than this package reading configuration itself:
	// a schedule is fixed for the life of the process (its field is
	// Restart-class), so the read belongs at the one point that has a boot
	// context. A zero value takes the Platform default.
	LibraryMaintenance app.LibraryMaintenanceSettings
	// Availability is the configured schedule for the watch-availability refresh
	// (roadmap M2.5), read at boot for the same reason and on the same terms.
	Availability app.AvailabilitySettings
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
	runner.Register(KindSessionTokenSweep, sessionTokenSweep(deps.Service))
	runner.Register(KindLibraryMaintenance, libraryMaintenance(deps.Service))
	runner.Register(KindLibraryAvailability, libraryAvailability(deps.Service))

	libraryEvery := deps.LibraryMaintenance.Interval
	if libraryEvery <= 0 {
		libraryEvery = app.DefaultLibraryMaintenance.Interval
	}
	availabilityEvery := deps.Availability.Interval
	if availabilityEvery <= 0 {
		availabilityEvery = app.DefaultAvailability.Interval
	}

	scheduler := jobs.NewScheduler(jobs.SchedulerDeps{
		Store: deps.Store,
		Clock: deps.Clock,
		IDs:   deps.IDs,
		Schedules: []jobs.Schedule{
			{Kind: KindTelemetryRetention, Every: TelemetryRetentionInterval},
			{Kind: KindSessionTokenSweep, Every: SessionTokenSweepInterval},
			{
				Kind:  KindLibraryMaintenance,
				Every: libraryEvery,
				// Fewer attempts than the default, because a retry here is not
				// free: the pass is idempotent, so re-running is safe, but each
				// attempt costs a source a round trip per item. A pass that
				// failed is better left to its next occurrence than hammered
				// four more times — the occurrence after it is hours away and
				// will do exactly the same work.
				MaxAttempts: 2,
			},
			{
				Kind:  KindLibraryAvailability,
				Every: availabilityEvery,
				// Two, for the maintenance pass's reason and one of its own: a
				// retry costs a provider a round trip per work, and a run that
				// failed loses nothing by waiting — the rows it did not reach
				// keep their old timestamps, so they are still at the head of
				// the queue when the next occurrence comes round.
				MaxAttempts: 2,
			},
		},
	})

	return Runtime{Runner: runner, Scheduler: scheduler}
}

// Start begins claiming and scheduling until ctx ends.
func (r Runtime) Start(ctx context.Context) {
	r.Runner.Start(ctx)
	r.Scheduler.Start(ctx)
}

// sessionTokenSweep is the handler for KindSessionTokenSweep. Idempotent for
// the same reason the retention sweep is: deleting rows that are already gone
// deletes nothing.
func sessionTokenSweep(svc *app.Service) jobs.Handler {
	return func(ctx context.Context, _ domain.Job) error {
		res, err := svc.PurgeSessionTokens(ctx, app.PurgeSessionTokensCommand{Caller: svc.SystemCaller()})
		if err != nil {
			return err
		}
		telemetry.From(ctx).Info("expired session tokens swept", telemetry.Int("rows", res.Deleted))
		return nil
	}
}

// libraryMaintenance is the handler for KindLibraryMaintenance (platform#60).
//
// It is idempotent in the way the runner requires and the way that matters
// here: materialising a title the library already holds is the no-op import the
// source binding already made it, and the enrichment pass fills only items with
// no Parts. A job reclaimed after a crash therefore repeats without adding a
// duplicate of anything.
//
// The job's id is forwarded so the pass can write a line per rule beside it.
// That is what makes the background-work screen an account of *why something is
// in the library* rather than a row saying a job succeeded.
func libraryMaintenance(svc *app.Service) jobs.Handler {
	return func(ctx context.Context, job domain.Job) error {
		res, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{
			Caller: svc.SystemCaller(), JobID: job.ID,
		})
		if err != nil {
			return err
		}
		telemetry.From(ctx).Info("library maintenance swept",
			telemetry.Int("rules", res.Rules),
			telemetry.Int("created", res.Created),
			telemetry.Int("refreshed", res.Refreshed),
			telemetry.Int("skipped", res.Skipped),
			telemetry.Int("failed", res.Failed))
		return nil
	}
}

// libraryAvailability is the handler for KindLibraryAvailability.
//
// Idempotent, and in the way that matters here rather than only in the way the
// runner requires: re-asking a provider about a title and storing the answer
// again is the same answer again. A job reclaimed after a crash repeats a
// bounded number of provider calls and changes nothing else.
func libraryAvailability(svc *app.Service) jobs.Handler {
	return func(ctx context.Context, _ domain.Job) error {
		res, err := svc.RefreshAvailability(ctx, app.RefreshAvailabilityCommand{
			Caller: svc.SystemCaller(),
		})
		if err != nil {
			return err
		}
		// Every work the run took is in exactly one of these. A run that reports
		// forty checked out of a budget of sixty, with nothing accounting for
		// the other twenty, is a log that raises a question instead of answering
		// one — the same accounting the maintenance pass keeps.
		telemetry.From(ctx).Info("watch availability refreshed",
			telemetry.Int("checked", res.Checked),
			telemetry.Int("skipped", res.Skipped),
			telemetry.Int("failed", res.Failed))
		return nil
	}
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
