// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// TelemetryQueryStore reads stored telemetry back (ADR 0058).
//
// It is read-only and it is **not on Tx**, which is the same call the write
// side made for the opposite reason. Telemetry is written outside any
// transaction because it must never fail a request; it is read outside one
// because a diagnostics query is a long, wide scan over a partitioned table
// and has no business holding a transaction open beside the work it is
// diagnosing.
type TelemetryQueryStore interface {
	// QueryLogs returns records matching filter, newest first.
	QueryLogs(ctx context.Context, filter domain.TelemetryLogFilter) ([]domain.TelemetryLogRecord, error)
	// Trace returns every span of one trace, oldest first, which is the order
	// a waterfall is drawn in.
	Trace(ctx context.Context, traceID string) ([]domain.TelemetrySpanRecord, error)
	// TraceLogs returns the log records belonging to one trace, oldest first.
	// This is the join that makes a trace legible: the spans say where the time
	// went, and these say what the code was saying while it went there.
	TraceLogs(ctx context.Context, traceID string) ([]domain.TelemetryLogRecord, error)
	// RecentTraces returns the most recent traces, one summary row each, for
	// the list a viewer opens on. Slow and failed traces are the ones anyone is
	// looking for, so they are what a caller can order by.
	RecentTraces(ctx context.Context, filter domain.TelemetryTraceFilter) ([]domain.TelemetryTraceSummary, error)
}
