// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// The queryable half of the dual sink (platform#36), against a real engine.
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
	if err := store.WriteBatch(ctx, records); err != nil {
		t.Fatalf("WriteBatch: %v", err)
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
	err := store.WriteBatch(ctx, []telemetry.Record{
		telemetryRecord(now, "connect failed", tc,
			telemetry.Secret("dsn", secret),
			telemetry.Sensitive("username", "alice@example.com"),
			// A struct literal, bypassing the constructors: its zero-value
			// class is not RedactionNone, so it must fail closed here too.
			telemetry.Field{Key: "raw", Value: secret},
		),
	})
	if err != nil {
		t.Fatalf("WriteBatch: %v", err)
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
	if err := store.WriteBatch(ctx, []telemetry.Record{
		telemetryRecord(now.AddDate(0, 0, -8), "ancient", old),
		telemetryRecord(now, "current", recent),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	dropped, err := store.DropExpiredPartitions(ctx, now, contracts.PartitionRetention{Logs: 3 * 24 * time.Hour, Spans: 3 * 24 * time.Hour})
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
	err := store.WriteBatch(ctx, []telemetry.Record{
		telemetryRecord(time.Now().UTC(), "nowhere to go", telemetry.NewTraceContext()),
	})
	if err == nil {
		t.Fatal("expected an error when no partition exists for the record's time")
	}
}

// TestSpanStoreWritesAWaterfall covers the span table end to end: a parent
// chain that reassembles into a tree, durations, and a failed span carrying its
// Platform error category.
func TestSpanStoreWritesAWaterfall(t *testing.T) {
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

	// Build a real tree with the real API rather than hand-assembling records,
	// so the parenting this asserts is the parenting the seams produce.
	captured := &captureSpanSink{}
	sctx := telemetry.WithSpanSink(ctx, captured)
	tc := telemetry.NewTraceContext()
	sctx = telemetry.TraceInto(sctx, tc)

	rpcCtx, rpc := telemetry.Start(sctx, "Invoke")
	txCtx, tx := telemetry.Start(rpcCtx, "tx")
	_, sql := telemetry.Start(txCtx, "sql INSERT nodes")
	sql.End()
	tx.End()
	_, mod := telemetry.Start(rpcCtx, "module.import")
	mod.Fail("unavailable", context.DeadlineExceeded)
	mod.End()
	rpc.End()

	if err := store.Spans().WriteBatch(ctx, captured.spans); err != nil {
		t.Fatalf("WriteBatch spans: %v", err)
	}

	// The waterfall query: every span of one trace, with its parent.
	rows, err := pool.Query(ctx,
		`SELECT name, parent, status, error_category FROM telemetry_spans
		  WHERE trace = $1 ORDER BY time`, tc.TraceIDString())
	if err != nil {
		t.Fatalf("query spans: %v", err)
	}
	defer rows.Close()

	type row struct{ name, parent, status, category string }
	got := map[string]row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.parent, &r.status, &r.category); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[r.name] = r
	}
	if len(got) != 4 {
		t.Fatalf("stored %d spans, want 4: %v", len(got), got)
	}
	if got["tx"].parent != rpc.TraceContext().SpanIDString() {
		t.Fatalf("tx parent = %q, want the Invoke span", got["tx"].parent)
	}
	if got["sql INSERT nodes"].parent != tx.TraceContext().SpanIDString() {
		t.Fatalf("sql parent = %q, want the tx span", got["sql INSERT nodes"].parent)
	}
	if got["module.import"].status != "error" || got["module.import"].category != "unavailable" {
		t.Fatalf("failed module span lost its status/category: %+v", got["module.import"])
	}
	// The outermost span here is not parentless: an edge continues the caller's
	// trace rather than starting a new one, so the RPC span's parent is the
	// client's span (platform#32). A parentless root would mean the Shell's half
	// of the trace had been thrown away at the wire.
	if got["Invoke"].parent != tc.SpanIDString() {
		t.Fatalf("the entry span should hang off the caller's span %q, got %q",
			tc.SpanIDString(), got["Invoke"].parent)
	}
}

// captureSpanSink collects spans so a test can hand a real tree to the store.
type captureSpanSink struct{ spans []telemetry.SpanRecord }

func (c *captureSpanSink) WriteSpan(r telemetry.SpanRecord) { c.spans = append(c.spans, r) }

// TestTransactionAndStatementSpansNest is seams 5 and 6 proven together against
// a real engine: the transaction span brackets the statements issued inside it.
//
// This is the shape that answers "where did the time go" — without the nesting,
// a waterfall shows a handler and a scatter of unattached queries, and the
// reader has to guess which belonged to which.
func TestTransactionAndStatementSpansNest(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	captured := &captureSpanSink{}
	ctx = telemetry.WithSpanSink(ctx, captured)
	ctx = telemetry.TraceInto(ctx, telemetry.NewTraceContext())

	set := postgres.Module{}.Bind(pool)
	err := set.UnitOfWork.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		_, err := tx.ModuleSettings().Set(ctx, domain.ModuleSettings{
			ModuleID: "stremio", Settings: []byte(`{"addons":[]}`), UpdatedAt: set.Clock.Now(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	var txSpan telemetry.SpanRecord
	var sqlSpans []telemetry.SpanRecord
	for _, s := range captured.spans {
		switch {
		case s.Name == "tx":
			txSpan = s
		case strings.HasPrefix(s.Name, "sql "):
			sqlSpans = append(sqlSpans, s)
		}
	}

	if txSpan.Name == "" {
		t.Fatalf("no transaction span recorded: %v", spanNames(captured.spans))
	}
	if txSpan.Status != "ok" {
		t.Fatalf("a committed transaction should not be an error span: %+v", txSpan)
	}
	// The outcome attribute is what distinguishes a commit from a rollback,
	// which are otherwise identical in a trace.
	if !hasAttr(txSpan, "db.outcome", "commit") {
		t.Fatalf("transaction span missing db.outcome=commit: %v", txSpan.Attributes)
	}
	if len(sqlSpans) == 0 {
		t.Fatalf("no statement spans recorded: %v", spanNames(captured.spans))
	}
	for _, s := range sqlSpans {
		if s.ParentID != txSpan.Trace.SpanIDString() {
			t.Fatalf("statement span %q hangs off %q, not the transaction %q",
				s.Name, s.ParentID, txSpan.Trace.SpanIDString())
		}
	}
}

// TestRollbackIsVisibleInItsSpan — "the write silently did not happen" is among
// the harder failures to chase, and a rolled-back transaction is otherwise
// indistinguishable from a committed one.
func TestRollbackIsVisibleInItsSpan(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	captured := &captureSpanSink{}
	ctx = telemetry.WithSpanSink(ctx, captured)
	ctx = telemetry.TraceInto(ctx, telemetry.NewTraceContext())

	set := postgres.Module{}.Bind(pool)
	wantErr := errors.New("caller changed its mind")
	if err := set.UnitOfWork.WithinTx(ctx, func(context.Context, contracts.Tx) error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("WithinTx err = %v, want the caller's error", err)
	}

	for _, s := range captured.spans {
		if s.Name != "tx" {
			continue
		}
		if s.Status != "error" || !hasAttr(s, "db.outcome", "rollback") {
			t.Fatalf("a rolled-back transaction must say so: status=%q attrs=%v", s.Status, s.Attributes)
		}
		return
	}
	t.Fatalf("no transaction span recorded: %v", spanNames(captured.spans))
}

func spanNames(spans []telemetry.SpanRecord) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name)
	}
	return out
}

func hasAttr(s telemetry.SpanRecord, key, value string) bool {
	for _, a := range s.Attributes {
		if a.Key == key {
			v, _ := a.EmitValue().(string)
			return v == value
		}
	}
	return false
}
