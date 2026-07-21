// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// TestComponentHealthReporterReflectsRealDatabaseState proves the
// Diagnostics exit criterion for the storage component directly: this
// is NOT a hardcoded "ok" — ReportHealth reflects a real, fully migrated
// PostgreSQL database when one is reachable, and reports Unavailable with a
// real reason once the connection is actually closed.
func TestComponentHealthReporterReflectsRealDatabaseState(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	reporter := postgres.NewComponentHealthReporter(pool)
	c := context.Background()

	healthy := reporter.ReportHealth(c)
	if healthy.Component != "postgres" {
		t.Fatalf("Component = %q, want %q", healthy.Component, "postgres")
	}
	if healthy.Health != domain.HealthHealthy {
		t.Fatalf("Health = %q, want %q against a fully migrated database", healthy.Health, domain.HealthHealthy)
	}
	if healthy.LastSuccessfulCheck.IsZero() {
		t.Fatal("expected a non-zero LastSuccessfulCheck after a healthy check")
	}
	if healthy.Lifecycle != domain.LifecycleRunning {
		t.Fatalf("Lifecycle = %q, want %q", healthy.Lifecycle, domain.LifecycleRunning)
	}
	firstSuccessfulCheck := healthy.LastSuccessfulCheck

	// Close the pool out from under the reporter: this is a real, live
	// connection failure, not a simulated one.
	pool.Close()

	unavailable := reporter.ReportHealth(c)
	if unavailable.Health != domain.HealthUnavailable {
		t.Fatalf("Health after closing the pool = %q, want %q", unavailable.Health, domain.HealthUnavailable)
	}
	if unavailable.DegradedReason == "" {
		t.Fatal("expected a non-empty DegradedReason once the database is unreachable")
	}
	if unavailable.LastFailureCategory == "" {
		t.Fatal("expected a non-empty LastFailureCategory once the database is unreachable")
	}
	// LastSuccessfulCheck must carry forward the last time it WAS healthy,
	// not reset to zero or advance just because a check ran.
	if !unavailable.LastSuccessfulCheck.Equal(firstSuccessfulCheck) {
		t.Fatalf("LastSuccessfulCheck after failure = %v, want unchanged %v", unavailable.LastSuccessfulCheck, firstSuccessfulCheck)
	}
}
