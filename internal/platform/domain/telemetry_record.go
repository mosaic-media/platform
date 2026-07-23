// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// The read models for stored telemetry (ADR 0058). These are what the
// expert-mode surface renders; they are deliberately separate from the write
// side in internal/platform/telemetry, which is about producing records and
// must stay free of anything that reads them back.
//
// Everything here is already redacted. Values were classified at construction
// and dropped or digested before storage (ADR 0056), so a reader applies no
// masking of its own — which is what makes rendering these into a browser
// defensible rather than alarming.

// TelemetryLogRecord is one stored log record.
type TelemetryLogRecord struct {
	Time      time.Time
	Level     string
	Service   string
	Instance  string
	Boot      string
	Trace     string
	Span      string
	Component string
	Module    string
	Message   string
	// Fields is the stored jsonb document, passed through as-is. The viewer
	// renders it; the Platform does not re-interpret what a call site chose to
	// record.
	Fields []byte
}

// TelemetrySpanRecord is one stored span.
type TelemetrySpanRecord struct {
	Time      time.Time
	Trace     string
	Span      string
	Parent    string
	Name      string
	Component string
	Module    string
	Service   string
	// Duration is the measured elapsed time.
	Duration time.Duration
	// Status is "ok" or "error".
	Status string
	// ErrorCategory is one of the Platform's seven categories, empty unless
	// the span failed.
	ErrorCategory string
	Attributes    []byte
}

// TelemetryLogFilter narrows a log query.
//
// Every field is optional and they combine with AND. There is deliberately no
// free-form query language (ADR 0058): filters and a trace lookup are the whole
// surface, because building a query language is how a diagnostics screen
// becomes a year of work, and anyone who needs one should export to a tool that
// already has it.
type TelemetryLogFilter struct {
	// Since and Until bound the window. A zero Until means "now".
	Since time.Time
	Until time.Time
	// MinLevel keeps records at or above a severity ("debug", "info", "warn",
	// "error"). Empty means all.
	MinLevel string
	// Component, Module and Trace are exact matches when set.
	Component string
	Module    string
	Trace     string
	// Contains is a case-insensitive substring of the message. It matches the
	// message only, never the fields: a field may hold a digest or a redaction
	// placeholder, and letting a search reach into them invites someone to
	// probe for values that were deliberately not stored.
	Contains string
	// Limit caps the rows returned. The store applies its own ceiling, so a
	// caller cannot ask for an unbounded page.
	Limit int
}

// TelemetryTraceSummary is one row in the trace list: enough to decide whether
// a trace is worth opening, without fetching its spans.
type TelemetryTraceSummary struct {
	Trace string
	// Root is the name of the trace's entry span — the operation a person
	// recognises ("/mosaic.session.v1.SessionService/Navigate").
	Root string
	// StartedAt is when the entry span began.
	StartedAt time.Time
	// Duration is the entry span's own duration, which is the whole operation
	// as the caller experienced it.
	Duration time.Duration
	// Spans counts the spans in the trace.
	Spans int
	// Errors counts the failed ones. A trace can succeed overall while
	// something inside it failed and was recovered — search swallowing an
	// unreachable addon is exactly that — so this is not implied by Status.
	Errors int
	// Status is the entry span's status.
	Status string
}

// TelemetryTraceOrder names how the trace list is sorted.
type TelemetryTraceOrder string

const (
	// TraceOrderRecent is newest first — the default, and what someone
	// reproducing a problem right now wants.
	TraceOrderRecent TelemetryTraceOrder = "recent"
	// TraceOrderSlowest is longest first.
	TraceOrderSlowest TelemetryTraceOrder = "slowest"
)

// TelemetryTraceFilter narrows the trace list.
type TelemetryTraceFilter struct {
	Since time.Time
	Until time.Time
	// FailedOnly keeps traces whose entry span failed or that contain a failed
	// span.
	FailedOnly bool
	Order      TelemetryTraceOrder
	Limit      int
}
