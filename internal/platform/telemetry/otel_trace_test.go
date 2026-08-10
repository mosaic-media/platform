// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry_test

import (
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// The trace id is Mosaic's correlation id (platform#32), so the conversion has to
// preserve *the same* id rather than produce one — a distinction any test
// asserting "an id exists" would miss entirely.
func TestTraceContextRoundTripsThroughOpenTelemetry(t *testing.T) {
	original := telemetry.NewTraceContext()

	back := telemetry.TraceContextOf(original.SpanContext())

	if back.TraceID != original.TraceID {
		t.Errorf("trace id is %x, want %x", back.TraceID, original.TraceID)
	}
	if back.SpanID != original.SpanID {
		t.Errorf("span id is %x, want %x", back.SpanID, original.SpanID)
	}
	if back.Sampled != original.Sampled {
		t.Errorf("sampled is %v, want %v", back.Sampled, original.Sampled)
	}
	// The rendered forms are what a person reads in the expert-mode viewer and
	// what a support report quotes, so they are worth asserting separately from
	// the bytes.
	if back.TraceIDString() != original.TraceIDString() {
		t.Errorf("rendered trace id is %q, want %q", back.TraceIDString(), original.TraceIDString())
	}
	if back.Traceparent() != original.Traceparent() {
		t.Errorf("traceparent is %q, want %q", back.Traceparent(), original.Traceparent())
	}
}

// **A root trace's span id is deliberately zero** — NewRootTrace means "nothing
// precedes this", and Start turns that into an empty ParentID rather than a run
// of zeros that looks like a real id. It has to survive the conversion, or a
// root span comes back claiming a parent that never existed.
func TestARootTracesZeroSpanIDSurvivesTheConversion(t *testing.T) {
	root := telemetry.NewRootTrace()
	if root.SpanID != [8]byte{} {
		t.Fatalf("NewRootTrace minted span id %x; this test is checking the wrong thing", root.SpanID)
	}

	back := telemetry.TraceContextOf(root.SpanContext())

	if back.SpanID != [8]byte{} {
		t.Errorf("span id is %x, want the zero that means no parent", back.SpanID)
	}
	if back.SpanIDString() != "" {
		t.Errorf("rendered span id is %q, want empty so a root span records no parent", back.SpanIDString())
	}
	if back.TraceID != root.TraceID {
		t.Errorf("trace id changed: %x, want %x", back.TraceID, root.TraceID)
	}
}

// The sampling decision governs whether spans are recorded and never whether
// the ids exist (platform#32), so both settings have to cross intact.
func TestTheSamplingDecisionCrossesBothWays(t *testing.T) {
	for _, sampled := range []bool{true, false} {
		tc := telemetry.NewTraceContext()
		tc.Sampled = sampled

		sc := tc.SpanContext()
		if sc.IsSampled() != sampled {
			t.Errorf("OTel sampled is %v, want %v", sc.IsSampled(), sampled)
		}
		if back := telemetry.TraceContextOf(sc); back.Sampled != sampled {
			t.Errorf("round-tripped sampled is %v, want %v", back.Sampled, sampled)
		}
		// And the ids are there either way, which is the property the record
		// states: an unsampled trace still joins a support report to the logs.
		if !sc.TraceID().IsValid() {
			t.Error("an unsampled trace lost its trace id")
		}
	}
}

// A traceparent parsed from the wire has to reach OTel unchanged, because this
// is the edge where an off-the-shelf client's header meets Mosaic's own trace.
func TestATraceparentFromTheWireReachesOpenTelemetryIntact(t *testing.T) {
	const header = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	parsed, ok := telemetry.ParseTraceparent(header)
	if !ok {
		t.Fatalf("ParseTraceparent(%q) refused it", header)
	}
	sc := parsed.SpanContext()

	if got := sc.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("OTel trace id is %q", got)
	}
	if got := sc.SpanID().String(); got != "00f067aa0ba902b7" {
		t.Errorf("OTel span id is %q", got)
	}
	if !sc.IsSampled() {
		t.Error("the sampled flag was dropped")
	}
	// OTel considers this a complete, usable context — which is what lets a
	// span started from it join the caller's trace rather than beginning a new
	// one.
	if !sc.IsValid() {
		t.Error("OTel does not consider the parsed context valid")
	}
	if back := telemetry.TraceContextOf(sc); back.Traceparent() != header {
		t.Errorf("round trip through OTel produced %q, want %q", back.Traceparent(), header)
	}
}

// OTel's own zero value must not read as a real trace, or an uninstrumented
// path would start reporting a trace of all zeros as though it were one.
func TestAnEmptyOTelContextIsNotAValidTrace(t *testing.T) {
	back := telemetry.TraceContextOf(trace.SpanContext{})
	if back.Valid() {
		t.Fatalf("an empty OTel context read as a valid trace: %+v", back)
	}
}
