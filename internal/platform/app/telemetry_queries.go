// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The policy actions for stored telemetry (ADR 0058).
//
// These are a genuine escalation, unlike the preference actions beside them.
// Telemetry records what every user did — which screens they opened, which
// titles they searched for — and although values are redacted at construction
// (ADR 0056), the *shape* of someone's activity is still visible. Granting
// telemetry.read is a decision about trust, not a convenience.
//
// They belong to the superuser and to nobody else by default (ADR 0069). The
// *first* account holds them — withholding an action from the only account that
// exists would create a permission nobody could ever be granted — while an
// administrator it allocates must be given them individually.
const (
	ActionTelemetryRead      policy.Action = "telemetry.read"
	ActionTelemetryExport    policy.Action = "telemetry.export"
	ActionTelemetryConfigure policy.Action = "telemetry.configure"
)

// QueryTelemetryLogsQuery reads stored log records.
type QueryTelemetryLogsQuery struct {
	Caller v1.Caller
	Filter domain.TelemetryLogFilter
}

// QueryTelemetryLogsResult is the matching records, newest first.
type QueryTelemetryLogsResult struct {
	Records []domain.TelemetryLogRecord
}

// QueryTelemetryLogs returns stored log records matching a filter.
func (s *Service) QueryTelemetryLogs(ctx context.Context, q QueryTelemetryLogsQuery) (QueryTelemetryLogsResult, error) {
	if err := s.authorizeTelemetryRead(ctx, q.Caller); err != nil {
		return QueryTelemetryLogsResult{}, err
	}
	records, err := s.telemetryQueries.QueryLogs(ctx, q.Filter)
	if err != nil {
		return QueryTelemetryLogsResult{}, err
	}
	return QueryTelemetryLogsResult{Records: records}, nil
}

// GetTraceQuery reads one trace: its spans, and the log records emitted inside
// them.
type GetTraceQuery struct {
	Caller  v1.Caller
	TraceID string
}

// GetTraceResult is the waterfall and its narration.
type GetTraceResult struct {
	Spans []domain.TelemetrySpanRecord
	Logs  []domain.TelemetryLogRecord
}

// GetTrace returns everything stored about one trace.
//
// Spans and logs together, in one call, because they answer the question
// jointly: the spans say where the time went and the logs say what the code
// was saying while it went there. Fetching them separately would make the
// viewer stitch on the client what the store can join.
func (s *Service) GetTrace(ctx context.Context, q GetTraceQuery) (GetTraceResult, error) {
	if err := s.authorizeTelemetryRead(ctx, q.Caller); err != nil {
		return GetTraceResult{}, err
	}
	if q.TraceID == "" {
		return GetTraceResult{}, contracts.NewError(contracts.InvalidArgument, "trace id is required")
	}

	spans, err := s.telemetryQueries.Trace(ctx, q.TraceID)
	if err != nil {
		return GetTraceResult{}, err
	}
	logs, err := s.telemetryQueries.TraceLogs(ctx, q.TraceID)
	if err != nil {
		return GetTraceResult{}, err
	}
	if len(spans) == 0 && len(logs) == 0 {
		// A trace id that matches nothing is NotFound rather than an empty
		// result: the usual way to reach this is pasting an id from a bug
		// report, and "no such trace" and "that trace did nothing" are
		// different answers to that.
		return GetTraceResult{}, contracts.NewError(contracts.NotFound, "no telemetry stored for that trace")
	}
	return GetTraceResult{Spans: spans, Logs: logs}, nil
}

// ListTracesQuery reads the recent-trace list.
type ListTracesQuery struct {
	Caller v1.Caller
	Filter domain.TelemetryTraceFilter
}

// ListTracesResult is one summary row per trace.
type ListTracesResult struct {
	Traces []domain.TelemetryTraceSummary
}

// ListTraces returns recent traces, newest or slowest first.
func (s *Service) ListTraces(ctx context.Context, q ListTracesQuery) (ListTracesResult, error) {
	if err := s.authorizeTelemetryRead(ctx, q.Caller); err != nil {
		return ListTracesResult{}, err
	}
	traces, err := s.telemetryQueries.RecentTraces(ctx, q.Filter)
	if err != nil {
		return ListTracesResult{}, err
	}
	return ListTracesResult{Traces: traces}, nil
}

// authorizeTelemetryRead is the shared gate for the three reads above. It is
// the command boundary's authenticate-then-authorize, factored out because
// three queries share one action and one resource.
func (s *Service) authorizeTelemetryRead(ctx context.Context, caller v1.Caller) error {
	if s.telemetryQueries == nil {
		// A Service built without the reader — every test that does not
		// exercise diagnostics. Unavailable rather than a nil dereference.
		return contracts.NewError(contracts.Unavailable, "telemetry queries are not configured")
	}
	_, err := s.enter(ctx, caller, ActionTelemetryRead, policy.Resource{Type: "telemetry"})
	return err
}

// CallerCan reports whether a caller holds an action, for deciding what to
// *draw* rather than what to permit.
//
// It exists because an affordance nobody may use should not be rendered: the
// expert-mode toggle is shown only to a caller who holds ActionTelemetryRead,
// so a normal user never sees a switch for a surface they cannot open. That is
// a stricter rule than ADR 0058 first wrote, and a better one — the record had
// the toggle visible to everyone and the data denied, which means routinely
// showing people a control that fails.
//
// It does not authorise and must never be mistaken for authorisation. It
// returns no authorized, so nothing downstream can mistake its answer for the
// proof ADR 0066 requires — the screens and services behind the affordance each
// run the real check, and this only suppresses a button. It fails closed, and
// it deliberately does not require permission.read, because asking "may I?"
// about oneself is not reading the permission system.
func (s *Service) CallerCan(ctx context.Context, caller v1.Caller, action policy.Action, resourceType string) bool {
	// It authenticates rather than taking an authorized (ADR 0066): the
	// emit-side holds a v1.Caller and has not entered the boundary for this
	// action — asking whether it *could* is the whole question.
	userID, err := s.authenticateCaller(ctx, caller)
	if err != nil {
		return false
	}
	decision, err := s.policy.Authorize(ctx, policy.Subject{UserID: userID}, action,
		policy.Resource{Type: resourceType}, policy.PolicyContext{})
	if err != nil {
		return false
	}
	return decision.Allowed
}
