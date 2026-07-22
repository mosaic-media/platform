// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// telemetryTable is the partitioned parent created by migration 0014.
const telemetryTable = "telemetry_logs"

// partitionLayout is the daily suffix: telemetry_logs_20260722.
const partitionLayout = "20060102"

// TelemetryStore writes telemetry records to PostgreSQL and manages the
// partitions they live in (ADR 0058).
//
// It is deliberately not a contracts.* store on Tx. Every other store here
// exists to make state and its outbox event commit together; this one exists
// to make records queryable, must never fail a request, and is written from a
// background goroutine outside any transaction. Putting it on Tx would promise
// atomicity nothing wants and would let a telemetry failure roll back real
// work.
type TelemetryStore struct {
	pool *pgxpool.Pool
}

// NewTelemetryStore builds a pool-backed telemetry writer.
func NewTelemetryStore(pool *pgxpool.Pool) *TelemetryStore {
	return &TelemetryStore{pool: pool}
}

// WriteRecords inserts a batch. It satisfies telemetry.BatchWriter.
//
// CopyFrom rather than a multi-row INSERT: this is the one write path in the
// Platform whose volume is unbounded by user action, and the binary copy
// protocol costs materially less per row. The trade is that CopyFrom gives no
// per-row error detail — which is the right trade here, because there is no
// caller in a position to act on one.
func (s *TelemetryStore) WriteRecords(ctx context.Context, records []telemetry.Record) error {
	if len(records) == 0 {
		return nil
	}

	rows := make([][]any, 0, len(records))
	for _, r := range records {
		fields, err := marshalFields(r.Fields)
		if err != nil {
			// One unserialisable record must not cost the batch. Skip it and
			// keep the rest: the file sink has it regardless.
			continue
		}
		rows = append(rows, []any{
			r.Time.UTC(),
			r.Level.String(),
			r.Resource.ServiceName,
			r.Resource.InstanceID,
			r.Resource.BootID,
			r.Trace.TraceIDString(),
			r.Trace.SpanIDString(),
			r.Component,
			r.Module,
			r.Message,
			fields,
		})
	}
	if len(rows) == 0 {
		return nil
	}

	_, err := s.pool.CopyFrom(ctx,
		pgx.Identifier{telemetryTable},
		[]string{"time", "level", "service", "instance", "boot", "trace", "span", "component", "module", "message", "fields"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return mapError("write telemetry records", err)
	}
	return nil
}

// marshalFields renders a record's fields as the jsonb document. Values are
// already redacted — Sensitive and Secret dropped their content at
// construction (ADR 0056) — so this serialises what is safe to store by the
// time it arrives, and performs no masking of its own.
func marshalFields(fields []telemetry.Field) ([]byte, error) {
	if len(fields) == 0 {
		return []byte("{}"), nil
	}
	doc := make(map[string]any, len(fields))
	for _, f := range fields {
		doc[f.Key] = f.EmitValue()
	}
	return json.Marshal(doc)
}

// EnsurePartitions creates the daily partitions covering [day, day+ahead).
//
// Partitions are created ahead of need rather than on demand, because the
// alternative is discovering at midnight that today has nowhere to go — and
// the failure mode of a missing partition is every insert in the batch
// failing, which is exactly when telemetry is least able to report it.
//
// This belongs in a scheduled job. The jobs runner, a scheduler and the system
// principal do not exist (ADR 0017, ADR 0058), so the composition root calls
// this at boot and on a ticker instead. That is a stated interim, and it is
// why `ahead` is generous: a process that runs for a week without restarting
// must not run out.
func (s *TelemetryStore) EnsurePartitions(ctx context.Context, day time.Time, ahead int) error {
	day = day.UTC().Truncate(24 * time.Hour)
	for i := 0; i < ahead; i++ {
		start := day.AddDate(0, 0, i)
		end := start.AddDate(0, 0, 1)
		name := partitionName(start)
		// Identifiers are derived from a date this code formats, never from
		// input, so the interpolation cannot carry anything a caller chose.
		sql := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
			name, telemetryTable,
			start.Format(time.RFC3339), end.Format(time.RFC3339),
		)
		if _, err := s.pool.Exec(ctx, sql); err != nil {
			return mapError("create telemetry partition", err)
		}
	}
	return nil
}

// DropExpiredPartitions removes every partition wholly older than retention,
// returning how many it dropped.
//
// DROP TABLE, not DELETE. That is the entire reason this table is partitioned:
// deleting a day of rows rewrites and vacuums a table an administrator may be
// querying, while dropping a partition is a catalogue update that finishes
// instantly and returns the disk immediately.
func (s *TelemetryStore) DropExpiredPartitions(ctx context.Context, now time.Time, retention time.Duration) (int, error) {
	cutoff := now.UTC().Add(-retention).Truncate(24 * time.Hour)

	rows, err := s.pool.Query(ctx,
		`SELECT c.relname
		   FROM pg_class c
		   JOIN pg_inherits i ON i.inhrelid = c.oid
		   JOIN pg_class p ON p.oid = i.inhparent
		  WHERE p.relname = $1`, telemetryTable)
	if err != nil {
		return 0, mapError("list telemetry partitions", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, mapError("scan telemetry partition", err)
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, mapError("list telemetry partitions", err)
	}

	dropped := 0
	for _, name := range names {
		day, ok := partitionDay(name)
		if !ok {
			// Not one of ours by name. Left alone: dropping a table because
			// its name did not parse is not a risk worth taking.
			continue
		}
		// Strictly before the cutoff day, so the partition holding the cutoff
		// itself is kept — retention is a floor on what is available, and
		// rounding it the other way would silently under-retain by a day.
		if !day.Before(cutoff) {
			continue
		}
		if _, err := s.pool.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, name)); err != nil {
			return dropped, mapError("drop telemetry partition", err)
		}
		dropped++
	}
	return dropped, nil
}

// partitionName is the table name for a day's partition.
func partitionName(day time.Time) string {
	return telemetryTable + "_" + day.UTC().Format(partitionLayout)
}

// partitionDay parses a partition name back to its day, reporting whether the
// name is one this code produced.
func partitionDay(name string) (time.Time, bool) {
	prefix := telemetryTable + "_"
	if len(name) != len(prefix)+len(partitionLayout) || name[:len(prefix)] != prefix {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation(partitionLayout, name[len(prefix):], time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}
