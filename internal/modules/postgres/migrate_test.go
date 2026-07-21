// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// TestMigrateFromEmptyDatabase exercises the migration gate: migrations run
// against a fresh install and the database is detectably migrated afterwards.
func TestMigrateFromEmptyDatabase(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	c := context.Background()

	// Before migrating, an empty database is detectably not migrated.
	if err := postgres.VerifyMigrated(c, pool); contracts.CategoryOf(err) != contracts.Unavailable {
		t.Fatalf("empty DB VerifyMigrated = %v, want Unavailable", err)
	}

	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	status, err := postgres.Status(c, pool)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.FullyMigrated() {
		t.Fatalf("not fully migrated: %+v", status)
	}
	if err := postgres.VerifyMigrated(c, pool); err != nil {
		t.Fatalf("VerifyMigrated after migrate: %v", err)
	}

	// A representative table from each end of the migration set exists.
	for _, table := range []string{"users", "sessions", "roles", "config_versions", "event_outbox", "jobs", "component_health_snapshots", "object_records"} {
		var exists bool
		if err := pool.QueryRow(c, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected table %q to exist after migration", table)
		}
	}
}

// TestMigrateIsIdempotent verifies re-running against an already-migrated
// database is a no-op success.
func TestMigrateIsIdempotent(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	c := context.Background()

	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	var afterFirst int
	if err := pool.QueryRow(c, `SELECT count(*) FROM platform_schema_migrations`).Scan(&afterFirst); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if afterFirst == 0 {
		t.Fatal("expected migrations to be applied")
	}

	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("second Migrate should be a no-op: %v", err)
	}
	var afterSecond int
	if err := pool.QueryRow(c, `SELECT count(*) FROM platform_schema_migrations`).Scan(&afterSecond); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	// Idempotency: re-running applies nothing new.
	if afterSecond != afterFirst {
		t.Fatalf("migration count changed on re-run: %d -> %d", afterFirst, afterSecond)
	}
}

// TestMigrateDetectsIncompatibleChecksum: an applied migration whose recorded
// checksum differs from this binary's definition must fail fast.
func TestMigrateDetectsIncompatibleChecksum(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	c := context.Background()

	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Corrupt the recorded checksum of version 1 to simulate a schema built
	// from a different migration definition.
	if _, err := pool.Exec(c, `UPDATE platform_schema_migrations SET checksum = 'corrupted' WHERE version = 1`); err != nil {
		t.Fatalf("corrupt checksum: %v", err)
	}

	err := postgres.Migrate(c, pool)
	if contracts.CategoryOf(err) != contracts.Unavailable {
		t.Fatalf("Migrate = %v, want Unavailable", err)
	}
	if !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("error should mention incompatibility: %v", err)
	}
}

// TestMigrateDetectsPartialApplication: a gap in the applied version sequence
// (an earlier migration missing while a later one is present) must fail fast.
func TestMigrateDetectsPartialApplication(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	c := context.Background()

	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Delete an earlier migration's record, leaving a gap below the highest
	// applied version — the fingerprint of an interrupted/partial apply.
	if _, err := pool.Exec(c, `DELETE FROM platform_schema_migrations WHERE version = 1`); err != nil {
		t.Fatalf("delete migration row: %v", err)
	}

	err := postgres.Migrate(c, pool)
	if contracts.CategoryOf(err) != contracts.Unavailable {
		t.Fatalf("Migrate = %v, want Unavailable", err)
	}
	if !strings.Contains(err.Error(), "partially applied") {
		t.Fatalf("error should mention partial application: %v", err)
	}
}

// TestMigrateDetectsDatabaseAhead: an applied migration version this binary
// does not know about means the database was migrated by a newer binary; the
// older binary must refuse to run against it.
func TestMigrateDetectsDatabaseAhead(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	c := context.Background()

	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Record a far-future migration version this binary does not embed. A
	// large number keeps the test valid as real migrations are added.
	if _, err := pool.Exec(c,
		`INSERT INTO platform_schema_migrations (version, name, checksum) VALUES (9999, 'future', 'future-checksum')`,
	); err != nil {
		t.Fatalf("insert future migration: %v", err)
	}

	err := postgres.Migrate(c, pool)
	if contracts.CategoryOf(err) != contracts.Unavailable {
		t.Fatalf("Migrate = %v, want Unavailable", err)
	}
	if !strings.Contains(err.Error(), "ahead") {
		t.Fatalf("error should mention database being ahead: %v", err)
	}
}
