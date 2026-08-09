// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"go.opentelemetry.io/otel/trace"
)

// The bridge between Mosaic's TraceContext and OpenTelemetry's SpanContext
// (ADR 0128).
//
// **This is the first piece of the Platform's conversion, and it is deliberately
// the first**, because it is the piece that must not be got wrong. The trace id
// *is* Mosaic's correlation id (ADR 0054): it is on every log record, every span,
// every event and every outbox row, and it is what joins a support report to the
// logs. A conversion that produced *a* trace id rather than *the same* trace id
// would pass any test asserting one exists, and would silently sever the join
// that the whole telemetry thread is arranged around.
//
// **The two representations are the same three values**, which is not luck:
// ADR 0054 chose W3C Trace Context precisely so "a future OTLP export, and any
// off-the-shelf instrumentation, work without a translation layer at each
// boundary". This file is that promise being collected — a field-for-field
// mapping with nothing invented and nothing dropped.
//
// It is tested in both directions and round-trip, so the rest of the conversion
// can be mechanical on top of a bridge that is already proven.

// SpanContext renders tc as OpenTelemetry's equivalent.
//
// The trace flags carry the sampling decision and nothing else, matching what
// Traceparent already writes on the wire. Remote is deliberately **not** set:
// this is used for a context this process holds, and OTel's own propagator sets
// the remote flag when it extracts one from a header.
func (tc TraceContext) SpanContext() trace.SpanContext {
	var flags trace.TraceFlags
	if tc.Sampled {
		flags = trace.FlagsSampled
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID(tc.TraceID),
		SpanID:     trace.SpanID(tc.SpanID),
		TraceFlags: flags,
	})
}

// TraceContextOf reads an OpenTelemetry span context back into Mosaic's.
//
// **A zero span id survives the round trip, and that matters.** NewRootTrace
// mints a trace whose span id is deliberately zero, meaning "nothing precedes
// this" — so a root trace must not come back with an invented parent. OTel's
// SpanContext holds the zero span id happily; what it will not do is report
// itself `IsValid`, which is why this reads the fields directly rather than
// gating on validity.
func TraceContextOf(sc trace.SpanContext) TraceContext {
	return TraceContext{
		TraceID: [16]byte(sc.TraceID()),
		SpanID:  [8]byte(sc.SpanID()),
		Sampled: sc.IsSampled(),
	}
}
