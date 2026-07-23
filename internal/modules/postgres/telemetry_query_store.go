// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// maxTelemetryRows is the ceiling on any single telemetry query.
//
// A caller may ask for fewer and cannot ask for more. The viewer is a
// diagnostics surface on a self-hosted box, not an export tool: someone
// scrolling a log wants a page, and an unbounded query against a partitioned
// table is how a diagnostics screen becomes the incident.
const maxTelemetryRows = 500

// defaultTelemetryRows applies when a caller names no limit.
const defaultTelemetryRows = 100

// telemetryQueryStore reads stored telemetry back for the expert-mode surface.
type telemetryQueryStore struct {
	pool *pgxpool.Pool
}

// NewTelemetryQueryStore builds the pool-backed reader.
func NewTelemetryQueryStore(pool *pgxpool.Pool) contracts.TelemetryQueryStore {
	return &telemetryQueryStore{pool: pool}
}

// levelAtLeast maps a minimum level onto the set at or above it. A map rather
// than an ordinal comparison in SQL, because the column stores names and
// ordering them in the query would mean encoding severity twice.
var levelAtLeast = map[string][]string{
	"debug": {"debug", "info", "warn", "error"},
	"info":  {"info", "warn", "error"},
	"warn":  {"warn", "error"},
	"error": {"error"},
}

func (s *telemetryQueryStore) QueryLogs(ctx context.Context, f domain.TelemetryLogFilter) ([]domain.TelemetryLogRecord, error) {
	// Built by appending conditions with positional parameters, never by
	// interpolating a caller's value. Every filter below is attacker-reachable
	// through the viewer.
	var (
		conds []string
		args  []any
	)
	add := func(cond string, arg any) {
		args = append(args, arg)
		conds = append(conds, strings.Replace(cond, "?", "$"+strconv.Itoa(len(args)), 1))
	}

	// The time bound is always present, so the planner can prune partitions
	// rather than scanning every day retained.
	since, until := f.Since, f.Until
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour)
	}
	if until.IsZero() {
		until = time.Now()
	}
	add("time >= ?", since.UTC())
	add("time <= ?", until.UTC())

	if levels, ok := levelAtLeast[f.MinLevel]; ok {
		add("level = ANY(?)", levels)
	}
	if f.Component != "" {
		add("component = ?", f.Component)
	}
	if f.Module != "" {
		add("module = ?", f.Module)
	}
	if f.Trace != "" {
		add("trace = ?", f.Trace)
	}
	if f.Contains != "" {
		// Message only — never the fields. A field may hold a digest or a
		// placeholder, and a substring search across them would let someone
		// probe for values that were deliberately not stored.
		add("message ILIKE ?", "%"+escapeLike(f.Contains)+"%")
	}

	sql := `SELECT time, level, service, instance, boot, trace, span, component, module, message, fields
	          FROM telemetry_logs
	         WHERE ` + strings.Join(conds, " AND ") + `
	         ORDER BY time DESC
	         LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, capRows(f.Limit))

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapError("query telemetry logs", err)
	}
	defer rows.Close()

	var out []domain.TelemetryLogRecord
	for rows.Next() {
		var r domain.TelemetryLogRecord
		if err := rows.Scan(&r.Time, &r.Level, &r.Service, &r.Instance, &r.Boot,
			&r.Trace, &r.Span, &r.Component, &r.Module, &r.Message, &r.Fields); err != nil {
			return nil, mapError("scan telemetry log", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("query telemetry logs", err)
	}
	return out, nil
}

func (s *telemetryQueryStore) Trace(ctx context.Context, traceID string) ([]domain.TelemetrySpanRecord, error) {
	if traceID == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "trace id is required")
	}
	rows, err := s.pool.Query(ctx,
		`SELECT time, trace, span, parent, name, component, module, service,
		        duration_us, status, error_category, attributes
		   FROM telemetry_spans
		  WHERE trace = $1
		  ORDER BY time
		  LIMIT $2`, traceID, maxTelemetryRows)
	if err != nil {
		return nil, mapError("query trace", err)
	}
	defer rows.Close()

	var out []domain.TelemetrySpanRecord
	for rows.Next() {
		var (
			r          domain.TelemetrySpanRecord
			durationUS int64
		)
		if err := rows.Scan(&r.Time, &r.Trace, &r.Span, &r.Parent, &r.Name, &r.Component,
			&r.Module, &r.Service, &durationUS, &r.Status, &r.ErrorCategory, &r.Attributes); err != nil {
			return nil, mapError("scan span", err)
		}
		r.Duration = time.Duration(durationUS) * time.Microsecond
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("query trace", err)
	}
	return out, nil
}

func (s *telemetryQueryStore) TraceLogs(ctx context.Context, traceID string) ([]domain.TelemetryLogRecord, error) {
	if traceID == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "trace id is required")
	}
	rows, err := s.pool.Query(ctx,
		`SELECT time, level, service, instance, boot, trace, span, component, module, message, fields
		   FROM telemetry_logs
		  WHERE trace = $1
		  ORDER BY time
		  LIMIT $2`, traceID, maxTelemetryRows)
	if err != nil {
		return nil, mapError("query trace logs", err)
	}
	defer rows.Close()

	var out []domain.TelemetryLogRecord
	for rows.Next() {
		var r domain.TelemetryLogRecord
		if err := rows.Scan(&r.Time, &r.Level, &r.Service, &r.Instance, &r.Boot,
			&r.Trace, &r.Span, &r.Component, &r.Module, &r.Message, &r.Fields); err != nil {
			return nil, mapError("scan trace log", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("query trace logs", err)
	}
	return out, nil
}

func (s *telemetryQueryStore) RecentTraces(ctx context.Context, f domain.TelemetryTraceFilter) ([]domain.TelemetryTraceSummary, error) {
	since, until := f.Since, f.Until
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour)
	}
	if until.IsZero() {
		until = time.Now()
	}

	// The entry span is the one with no parent — the operation as the caller
	// experienced it. Its duration is the trace's duration; summing the spans
	// would double-count, since a child's time is inside its parent's.
	order := "entry.time DESC"
	if f.Order == domain.TraceOrderSlowest {
		order = "entry.duration_us DESC"
	}
	having := ""
	if f.FailedOnly {
		// Either the operation failed, or something inside it did and was
		// recovered — search swallowing an unreachable addon is exactly that,
		// and it is the more interesting of the two.
		having = ` AND (entry.status <> 'ok' OR agg.errors > 0)`
	}

	sql := `
	SELECT entry.trace, entry.name, entry.time, entry.duration_us, entry.status,
	       agg.spans, agg.errors
	  FROM telemetry_spans entry
	  JOIN (SELECT trace, count(*) AS spans,
	               count(*) FILTER (WHERE status <> 'ok') AS errors
	          FROM telemetry_spans
	         WHERE time >= $1 AND time <= $2
	         GROUP BY trace) agg ON agg.trace = entry.trace
	 WHERE entry.parent = '' AND entry.time >= $1 AND entry.time <= $2` + having + `
	 ORDER BY ` + order + `
	 LIMIT $3`

	rows, err := s.pool.Query(ctx, sql, since.UTC(), until.UTC(), capRows(f.Limit))
	if err != nil {
		return nil, mapError("query recent traces", err)
	}
	defer rows.Close()

	var out []domain.TelemetryTraceSummary
	for rows.Next() {
		var (
			t          domain.TelemetryTraceSummary
			durationUS int64
		)
		if err := rows.Scan(&t.Trace, &t.Root, &t.StartedAt, &durationUS, &t.Status, &t.Spans, &t.Errors); err != nil {
			return nil, mapError("scan trace summary", err)
		}
		t.Duration = time.Duration(durationUS) * time.Microsecond
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("query recent traces", err)
	}
	return out, nil
}

// capRows bounds a caller's limit.
func capRows(limit int) int {
	if limit <= 0 {
		return defaultTelemetryRows
	}
	if limit > maxTelemetryRows {
		return maxTelemetryRows
	}
	return limit
}

// escapeLike neutralises the wildcards in an ILIKE pattern, so a search for
// "100%" means that rather than "anything starting with 100".
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
