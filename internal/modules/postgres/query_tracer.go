// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// queryTracer emits a span per SQL statement (platform#33, seam 6).
//
// This is the seam that most often answers "where did the nine seconds go",
// and it costs nothing at any call site: pgx offers the hook, so no store, no
// service and no handler changes to get it. Every query the Platform issues —
// through a UnitOfWork or a direct pooled read — is covered because they all
// go through this pool.
//
// It records the SQL text. That is a considered decision rather than an
// oversight: Platform SQL is written in this repository as string constants,
// so it contains no user data — the values are always bound parameters, and
// those are *not* recorded. A span that showed the arguments would carry
// usernames and search terms straight into the telemetry store, which is
// precisely what platform#34 exists to prevent.
type queryTracer struct{}

// traceQueryKey carries the in-flight span between the pgx start and end hooks.
type traceQueryKey struct{}

// TraceQueryStart begins the statement's span.
func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := telemetry.Start(ctx, "sql "+statementName(data.SQL),
		telemetry.String("db.system", "postgresql"),
		// The statement, never its arguments — see the type comment.
		telemetry.String("db.statement", collapse(data.SQL)),
	)
	return context.WithValue(ctx, traceQueryKey{}, span)
}

// TraceQueryEnd completes it.
func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(traceQueryKey{}).(*telemetry.Span)
	if !ok || span == nil {
		return
	}
	if data.Err != nil {
		// No category: the store above decides one from context this hook does
		// not have (a no-rows result is NotFound for one caller and an empty
		// list for another). Recording a guess here would put a category on the
		// span that disagrees with the error the caller actually received,
		// which is worse than leaving it blank.
		span.Fail("", data.Err)
	} else {
		span.SetAttributes(telemetry.Int64("db.rows_affected", data.CommandTag.RowsAffected()))
	}
	span.End()
}

// statementName is a short label for the span: the verb and the table, so a
// waterfall reads "sql SELECT nodes" rather than repeating a whole query.
func statementName(sql string) string {
	fields := strings.Fields(collapse(sql))
	if len(fields) == 0 {
		return "query"
	}
	verb := strings.ToUpper(fields[0])
	// The word after FROM, INTO or UPDATE is the table for the shapes the
	// Platform actually issues. Anything less regular just gets its verb, which
	// is still more useful in a waterfall than nothing.
	for i, f := range fields {
		switch strings.ToUpper(f) {
		case "FROM", "INTO", "UPDATE":
			if i+1 < len(fields) {
				return verb + " " + strings.Trim(fields[i+1], `"(),`)
			}
		}
	}
	return verb
}

// collapse flattens the multi-line SQL the stores are written with into one
// line, so a span attribute is readable in a table cell.
func collapse(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}
