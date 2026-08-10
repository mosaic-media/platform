// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	compositionjobs "github.com/mosaic-media/platform/internal/composition/jobs"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The whole of M0.1 in one test, against real PostgreSQL: a recurring schedule
// enqueues an occurrence, a runner claims it with no user anywhere in the
// picture, the handler authorises as the system principal through the ordinary
// command boundary, and rows that retention has run out on are gone from the
// database afterwards.
//
// It is one test rather than four because the claim being made is that these
// compose. Each piece has its own test; what this asserts is that nothing
// between them requires a person.

// fixedClock lets the sweep be asked about a moment rather than about now.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestARecurringJobRunsWithNoUserAndRetentionRemovesRows(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	clock := fixedClock{now: time.Now().UTC().Truncate(24 * time.Hour)}

	svc := app.NewService(app.Deps{
		UnitOfWork:  cs.UnitOfWork,
		Sessions:    cs.Sessions,
		Users:       cs.Users,
		Credentials: cs.Credentials,
		Config:      cs.Config,
		Permissions: cs.Permissions,
		Nodes:       cs.Nodes,
		Clock:       clock,
		IDs:         cs.IDs,
		ContentIDs:  cs.ContentIDs,
		Policy:      policy.NewEngine(cs.Permissions),
		Events:      noopPublisher{},

		TelemetryQueries:     cs.TelemetryQueries,
		TelemetryMaintenance: cs.TelemetryMaintenance,
		Jobs:                 cs.Jobs,
	})

	// Ten days of partitions ending at the sweep's "today", and one record on
	// each side of a three-day retention line.
	store := postgres.NewTelemetryStore(pool)
	if err := store.EnsurePartitions(ctx, clock.now.AddDate(0, 0, -9), 10); err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}
	expired := telemetry.NewTraceContext()
	kept := telemetry.NewTraceContext()
	if err := store.WriteBatch(ctx, []telemetry.Record{
		telemetryRecord(clock.now.AddDate(0, 0, -8), "old enough to drop", expired),
		telemetryRecord(clock.now, "inside the window", kept),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if got := countByTrace(t, pool, expired); got != 1 {
		t.Fatalf("the expired record was not stored (%d rows)", got)
	}

	// Retention comes from the Active configuration, which is where an
	// administrator sets it (platform#36). Three days for both signals, so the
	// eight-day-old partition is outside it and today's is not. Nothing about
	// the sweep is told this directly — it reads the policy per run, which is
	// what makes those fields Hot.
	activateRetention(t, ctx, cs, clock.now, 3, 72)

	runtime := compositionjobs.New(compositionjobs.Deps{
		Service: svc,
		Store:   cs.Jobs,
		Clock:   clock,
		IDs:     cs.IDs,
		Owner:   "boot-under-test",
	})

	// The scheduler enqueues one occurrence of every recurring kind this build
	// carries. No caller, no session, no request anywhere in the picture.
	created, err := runtime.Scheduler.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if created == 0 {
		t.Fatal("the schedule enqueued nothing")
	}

	// And the runner runs them. Asserted against what was enqueued rather than
	// against a literal, so adding a recurring kind does not break this test
	// for a reason that has nothing to do with what it is about.
	ran, err := runtime.Runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if ran != created {
		t.Fatalf("the runner claimed %d of %d enqueued jobs", ran, created)
	}

	queued, err := cs.Jobs.List(ctx, domain.JobFilter{Kind: compositionjobs.KindTelemetryRetention})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("%d retention jobs recorded, want 1", len(queued))
	}
	if queued[0].Status != domain.JobSucceeded {
		t.Fatalf("the sweep ended %s (%s), want %s",
			queued[0].Status, queued[0].LastError, domain.JobSucceeded)
	}

	// The exit criterion, stated as a row count: the rows are gone from
	// PostgreSQL, and the ones inside the window are not.
	if got := countByTrace(t, pool, expired); got != 0 {
		t.Fatalf("%d expired rows survived the sweep", got)
	}
	if got := countByTrace(t, pool, kept); got != 1 {
		t.Fatalf("the sweep removed a record inside the window (%d rows remain)", got)
	}

	// The attempt and the log line are readable afterwards, which is what makes
	// the queue diagnosable rather than merely functional.
	attempts, err := cs.Jobs.Attempts(ctx, queued[0].ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("Attempts = %d, %v; want 1", len(attempts), err)
	}
	if attempts[0].Status != domain.JobAttemptSucceeded {
		t.Fatalf("attempt recorded as %s, want %s", attempts[0].Status, domain.JobAttemptSucceeded)
	}
	if attempts[0].Runner != "boot-under-test" {
		t.Fatalf("attempt names runner %q, want the boot id", attempts[0].Runner)
	}
	lines, err := cs.Jobs.Logs(ctx, queued[0].ID, 0)
	if err != nil || len(lines) != 1 {
		t.Fatalf("Logs = %d, %v; want 1", len(lines), err)
	}

	// Second tick, same occurrence: it is already queued and already done, so
	// nothing runs again. This is what stops an hourly sweep from running on
	// every two-second poll.
	if created, err := runtime.Scheduler.Tick(ctx); err != nil || created != 0 {
		t.Fatalf("a second tick in the same occurrence created %d jobs, %v", created, err)
	}
	if ran, err := runtime.Runner.RunOnce(ctx); err != nil || ran != 0 {
		t.Fatalf("a completed sweep was claimed again: %d, %v", ran, err)
	}
}

// The other half of the system principal: it is a principal, not a bypass. The
// sweep refuses a session holding no grants exactly as any other command does,
// and the *only* difference in the allowed call is who the caller is.
func TestTheRetentionSweepStillRefusesAnOrdinaryUngrantedCaller(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	now := cs.Clock.Now()
	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users,
		Credentials: cs.Credentials, Config: cs.Config, Permissions: cs.Permissions,
		Nodes: cs.Nodes, Clock: cs.Clock, IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{},
		TelemetryMaintenance: cs.TelemetryMaintenance, Jobs: cs.Jobs,
	})

	user, err := cs.Users.Create(ctx, domain.User{
		ID: "viewer", Username: "viewer", Email: "viewer@example.com",
		Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := cs.Sessions.Create(ctx, domain.Session{
		ID: "viewer-session", UserID: user.ID, DeviceID: "d",
		IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		AuthStrength: domain.AuthStrengthPassword,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if _, err := svc.PurgeTelemetry(ctx, app.PurgeTelemetryCommand{
		Caller: v1.Caller{Session: "viewer-session"},
	}); err == nil {
		t.Fatal("a caller holding no grants swept telemetry")
	}

	if _, err := svc.PurgeTelemetry(ctx, app.PurgeTelemetryCommand{
		Caller: svc.SystemCaller(),
	}); err != nil {
		t.Fatalf("the system principal was refused its own maintenance: %v", err)
	}
}

// countByTrace is the row count the exit criterion is stated in.
func countByTrace(t *testing.T, pool *pgxpool.Pool, tc telemetry.TraceContext) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM telemetry_logs WHERE trace = $1`, tc.TraceIDString()).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// activateRetention writes and activates a config version carrying the
// retention fields, which is how an administrator sets them.
func activateRetention(t *testing.T, ctx context.Context, cs *postgres.ContractSet, now time.Time, logDays, traceHours int) {
	t.Helper()
	payload := []byte(`{"telemetry.retention.logs_days": ` + strconv.Itoa(logDays) +
		`, "telemetry.retention.traces_hours": ` + strconv.Itoa(traceHours) + `}`)
	activated := now
	if _, err := cs.Config.Save(ctx, domain.ConfigVersion{
		ID: "config-retention", Payload: payload, Status: domain.ConfigActive,
		CreatedAt: now, ActivatedAt: &activated,
	}); err != nil {
		t.Fatalf("save config version: %v", err)
	}
}
