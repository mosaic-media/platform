// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// The queryable half of the dual sink (ADR 0058), against a real engine.
// Partitioning, BRIN indexes, CopyFrom and DROP-based retention are all things
// that either work in PostgreSQL or do not; a fake would only prove the test
// agrees with itself.

func telemetryRecord(at time.Time, msg string, tc telemetry.TraceContext, fields ...telemetry.Field) telemetry.Record {
	return telemetry.Record{
		Time:      at,
		Level:     telemetry.LevelInfo,
		Component: "session",
		Message:   msg,
		Fields:    fields,
		Resource:  telemetry.Resource{ServiceName: "mosaic-platform", InstanceID: "i-1", BootID: "b-1"},
		Trace:     tc,
	}
}

func TestTelemetryStoreWritesAndQueriesByTrace(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := postgres.NewTelemetryStore(pool)
	now := time.Now().UTC()
	if err := store.EnsurePartitions(ctx, now, 2); err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}

	tc := telemetry.NewTraceContext()
	other := telemetry.NewTraceContext()
	records := []telemetry.Record{
		telemetryRecord(now, "intent", tc, telemetry.String("procedure", "Navigate"), telemetry.Int("status", 200)),
		telemetryRecord(now, "stream open", tc, telemetry.Int64("resume", 7)),
		telemetryRecord(now, "unrelated", other),
	}
	if err := store.WriteRecords(ctx, records); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}

	// The correlation query — the single most important thing this table
	// serves, and the reason trace is a column rather than a key inside jsonb.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM telemetry_logs WHERE trace = $1`, tc.TraceIDString()).Scan(&count); err != nil {
		t.Fatalf("query by trace: %v", err)
	}
	if count != 2 {
		t.Fatalf("trace query returned %d rows, want 2", count)
	}

	// Typed field values must survive as JSON types, not stringified, or the
	// generalisation from string to any bought nothing at the storage layer.
	var status int
	if err := pool.QueryRow(ctx,
		`SELECT (fields->>'status')::int FROM telemetry_logs WHERE trace = $1 AND message = 'intent'`,
		tc.TraceIDString()).Scan(&status); err != nil {
		t.Fatalf("query field: %v", err)
	}
	if status != 200 {
		t.Fatalf("status field = %d, want 200", status)
	}
}

// TestTelemetryStoreRedactsAtTheStorageLayerToo is the property that makes
// rendering these rows into a browser defensible: a classified value must not
// reach the database, not merely be hidden when read back.
func TestTelemetryStoreNeverStoresAClassifiedValue(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := postgres.NewTelemetryStore(pool)
	now := time.Now().UTC()
	if err := store.EnsurePartitions(ctx, now, 2); err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}

	const secret = "hunter2-super-secret-password-AKIAFAKEEXAMPLE1234"
	tc := telemetry.NewTraceContext()
	err := store.WriteRecords(ctx, []telemetry.Record{
		telemetryRecord(now, "connect failed", tc,
			telemetry.Secret("dsn", secret),
			telemetry.Sensitive("username", "alice@example.com"),
			// A struct literal, bypassing the constructors: its zero-value
			// class is not RedactionNone, so it must fail closed here too.
			telemetry.Field{Key: "raw", Value: secret},
		),
	})
	if err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}

	var doc string
	if err := pool.QueryRow(ctx, `SELECT fields::text FROM telemetry_logs WHERE trace = $1`,
		tc.TraceIDString()).Scan(&doc); err != nil {
		t.Fatalf("read fields: %v", err)
	}
	for _, forbidden := range []string{secret, "alice@example.com"} {
		if strings.Contains(doc, forbidden) {
			t.Fatalf("a classified value reached the database: %s", doc)
		}
	}
}

// TestTelemetryRetentionDropsWholeOldPartitions covers the reason this table is
// partitioned at all: retention is a catalogue update, not a rewrite of a table
// somebody is querying.
func TestTelemetryRetentionDropsWholeOldPartitions(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := postgres.NewTelemetryStore(pool)
	now := time.Now().UTC().Truncate(24 * time.Hour)

	// Ten days ending today, so there is a clear old and new side.
	if err := store.EnsurePartitions(ctx, now.AddDate(0, 0, -9), 10); err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}

	old := telemetry.NewTraceContext()
	recent := telemetry.NewTraceContext()
	if err := store.WriteRecords(ctx, []telemetry.Record{
		telemetryRecord(now.AddDate(0, 0, -8), "ancient", old),
		telemetryRecord(now, "current", recent),
	}); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}

	dropped, err := store.DropExpiredPartitions(ctx, now, 3*24*time.Hour)
	if err != nil {
		t.Fatalf("DropExpiredPartitions: %v", err)
	}
	if dropped == 0 {
		t.Fatal("expected old partitions to be dropped")
	}

	var oldRows, recentRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_logs WHERE trace = $1`,
		old.TraceIDString()).Scan(&oldRows); err != nil {
		t.Fatalf("count old: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_logs WHERE trace = $1`,
		recent.TraceIDString()).Scan(&recentRows); err != nil {
		t.Fatalf("count recent: %v", err)
	}
	if oldRows != 0 {
		t.Fatalf("expected expired rows to be gone, found %d", oldRows)
	}
	if recentRows != 1 {
		t.Fatalf("retention removed a record inside the window: %d rows remain", recentRows)
	}
}

// TestEnsurePartitionsIsIdempotent — it runs at boot and hourly thereafter, so
// re-creating an existing day must be a no-op rather than an error.
func TestEnsurePartitionsIsIdempotent(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := postgres.NewTelemetryStore(pool)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := store.EnsurePartitions(ctx, now, 4); err != nil {
			t.Fatalf("EnsurePartitions (pass %d): %v", i, err)
		}
	}
}

// TestWriteRecordsWithoutAPartitionFailsRatherThanCorrupting: PostgreSQL
// refuses a row with no partition to hold it. This asserts the failure is
// surfaced as an error the buffered sink can count, not swallowed — the sink's
// Failed counter is the only thing that makes this visible in production.
func TestWriteRecordsWithoutAPartitionIsAnError(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := postgres.NewTelemetryStore(pool)
	// No EnsurePartitions call at all.
	err := store.WriteRecords(ctx, []telemetry.Record{
		telemetryRecord(time.Now().UTC(), "nowhere to go", telemetry.NewTraceContext()),
	})
	if err == nil {
		t.Fatal("expected an error when no partition exists for the record's time")
	}
}
