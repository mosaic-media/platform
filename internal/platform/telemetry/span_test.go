// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// captureSpans collects completed spans.
type captureSpans struct {
	mu    sync.Mutex
	spans []telemetry.SpanRecord
}

func (c *captureSpans) WriteSpan(r telemetry.SpanRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, r)
}

func (c *captureSpans) all() []telemetry.SpanRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]telemetry.SpanRecord(nil), c.spans...)
}

func (c *captureSpans) byName(name string) (telemetry.SpanRecord, bool) {
	for _, s := range c.all() {
		if s.Name == name {
			return s, true
		}
	}
	return telemetry.SpanRecord{}, false
}

// spanCtx returns a context wired to sink, with a trace already established.
func spanCtx(sink telemetry.SpanSink) (context.Context, telemetry.TraceContext) {
	tc := telemetry.NewTraceContext()
	ctx := telemetry.WithSpanSink(context.Background(), sink)
	return telemetry.TraceInto(ctx, tc), tc
}

// TestNestedSpansFormATree is the property the whole waterfall rests on: the
// context a span returns is what makes the next span its child, so the seams
// compose without any of them knowing about each other.
func TestNestedSpansFormATree(t *testing.T) {
	sink := &captureSpans{}
	ctx, tc := spanCtx(sink)

	ctx, outer := telemetry.Start(ctx, "rpc")
	inner1Ctx, inner1 := telemetry.Start(ctx, "tx")
	_, leaf := telemetry.Start(inner1Ctx, "sql SELECT nodes")
	leaf.End()
	inner1.End()
	outer.End()

	rpc, ok1 := sink.byName("rpc")
	tx, ok2 := sink.byName("tx")
	sql, ok3 := sink.byName("sql SELECT nodes")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("expected three spans, got %d", len(sink.all()))
	}

	// One trace throughout.
	for _, s := range []telemetry.SpanRecord{rpc, tx, sql} {
		if s.Trace.TraceIDString() != tc.TraceIDString() {
			t.Fatalf("span %q left the trace: %s", s.Name, s.Trace.TraceIDString())
		}
	}
	// And a real parent chain, which is what a waterfall is drawn from.
	if tx.ParentID != rpc.Trace.SpanIDString() {
		t.Fatalf("tx parent = %s, want the rpc span %s", tx.ParentID, rpc.Trace.SpanIDString())
	}
	if sql.ParentID != tx.Trace.SpanIDString() {
		t.Fatalf("sql parent = %s, want the tx span %s", sql.ParentID, tx.Trace.SpanIDString())
	}
}

// TestSpanRebindsTheLoggerSoLogsAgreeWithSpans guards a subtle mismatch: a log
// line emitted inside a span must carry that span's id, not its parent's, or
// the log and the trace agree on the request and disagree on where in it.
func TestSpanRebindsTheLoggerSoLogsAgreeWithSpans(t *testing.T) {
	sink := &captureSpans{}
	var buf strings.Builder
	logger := telemetry.New(telemetry.NewJSONSink(&buf), telemetry.Resource{}, telemetry.LevelDebug)

	ctx, _ := spanCtx(sink)
	ctx = telemetry.Into(ctx, logger)

	ctx, span := telemetry.Start(ctx, "work")
	telemetry.From(ctx).Info("inside the span")
	span.End()

	got, ok := sink.byName("work")
	if !ok {
		t.Fatal("no span recorded")
	}
	if !strings.Contains(buf.String(), `"span":"`+got.Trace.SpanIDString()+`"`) {
		t.Fatalf("log record does not carry the span id %s: %s", got.Trace.SpanIDString(), buf.String())
	}
}

func TestSpanRecordsFailureAndCategory(t *testing.T) {
	sink := &captureSpans{}
	ctx, _ := spanCtx(sink)

	_, span := telemetry.Start(ctx, "doomed")
	span.Fail("not_found", errors.New("no such node"))
	span.End()

	got, _ := sink.byName("doomed")
	if got.Status != "error" {
		t.Fatalf("status = %q, want error", got.Status)
	}
	if got.ErrorCategory != "not_found" {
		t.Fatalf("category = %q, want not_found", got.ErrorCategory)
	}
}

// TestSpanEndIsIdempotent — every seam uses `defer span.End()` and several also
// End explicitly on an error path, so a double End must not double-write.
func TestSpanEndIsIdempotent(t *testing.T) {
	sink := &captureSpans{}
	ctx, _ := spanCtx(sink)

	_, span := telemetry.Start(ctx, "once")
	span.End()
	span.End()
	span.End()

	if n := len(sink.all()); n != 1 {
		t.Fatalf("recorded %d spans for one End sequence, want 1", n)
	}
}

// TestSpanIsSafeWithoutAnySetup is what lets an instrumented code path be unit
// tested with no telemetry wiring at all: no sink, no trace, no logger.
func TestSpanIsSafeWithoutAnySetup(t *testing.T) {
	ctx, span := telemetry.Start(context.Background(), "orphan",
		telemetry.String("k", "v"))
	span.SetAttributes(telemetry.Int("n", 1))
	span.Fail("internal", errors.New("boom"))
	span.End()

	// It still mints a trace, so anything nested below is coherent even though
	// nothing is recorded.
	if tc, ok := telemetry.TraceFrom(ctx); !ok || !tc.Valid() {
		t.Fatal("an unconfigured span must still establish a usable trace context")
	}
}

// TestSpanAttributesObeyRedaction — a span attribute is not a second, laxer
// channel than a log field (ADR 0056).
func TestSpanAttributesObeyRedaction(t *testing.T) {
	sink := &captureSpans{}
	ctx, _ := spanCtx(sink)

	const secret = "hunter2-super-secret-password-AKIAFAKEEXAMPLE1234"
	_, span := telemetry.Start(ctx, "sensitive",
		telemetry.Secret("token", secret),
		telemetry.Sensitive("user", "alice@example.com"))
	span.End()

	got, _ := sink.byName("sensitive")
	for _, attr := range got.Attributes {
		if v, ok := attr.EmitValue().(string); ok && (strings.Contains(v, "hunter2") || strings.Contains(v, "alice")) {
			t.Fatalf("a classified value survived into a span attribute: %v", attr)
		}
	}
}

// TestUnendedSpanIsNeverRecorded: a panicking path costs a missing span rather
// than a span with a nonsense duration.
func TestUnendedSpanIsNeverRecorded(t *testing.T) {
	sink := &captureSpans{}
	ctx, _ := spanCtx(sink)

	_, _ = telemetry.Start(ctx, "abandoned")
	if n := len(sink.all()); n != 0 {
		t.Fatalf("an unended span was recorded (%d spans)", n)
	}
}

// One journey, end to end, with nothing losing coherence in the middle of it.
//
// **This is the property the conversion to OpenTelemetry had to preserve**
// (ADR 0128), and it is asserted over a whole request rather than a pair of
// spans: an inbound traceparent from a client, an entry span, a handler, a
// module invocation and a SQL statement, with a log record emitted at every
// level. Every one of them must carry the *same* trace id — a conversion
// producing a valid trace id per hop would pass every test that checks one
// exists, and would leave a support report joinable to nothing.
func TestAWholeJourneyKeepsOneTraceAcrossSpansAndLogs(t *testing.T) {
	caller := telemetry.NewTraceContext()
	sink := &captureSpans{}
	var logs strings.Builder

	// The edge, as a client reaches it.
	ctx := telemetry.StartRequest(context.Background(), caller.Traceparent())
	ctx = telemetry.WithSpanSink(ctx, sink)
	ctx = telemetry.Into(ctx, telemetry.New(telemetry.NewJSONSink(&logs), telemetry.Resource{}, telemetry.LevelDebug))

	rpcCtx, rpc := telemetry.Start(ctx, "rpc")
	telemetry.From(rpcCtx).Info("handling")

	handlerCtx, handler := telemetry.Start(rpcCtx, "handler")
	telemetry.From(handlerCtx).Info("authorised")

	moduleCtx, module := telemetry.Start(handlerCtx, "module.tmdb.Import")
	telemetry.From(moduleCtx).Info("fetching")

	sqlCtx, sql := telemetry.Start(moduleCtx, "sql INSERT nodes")
	telemetry.From(sqlCtx).Info("inserted")

	sql.End()
	module.End()
	handler.End()
	rpc.End()

	recorded := sink.all()
	if len(recorded) != 4 {
		t.Fatalf("want four spans, got %d", len(recorded))
	}

	// One trace, from the caller's header all the way to the statement.
	want := caller.TraceIDString()
	for _, s := range recorded {
		if got := s.Trace.TraceIDString(); got != want {
			t.Errorf("span %q is on trace %s, want the caller's %s", s.Name, got, want)
		}
	}
	// Every log line too, which is the half a waterfall cannot show and a
	// support report is actually read from.
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if !strings.Contains(line, `"trace":"`+want+`"`) {
			t.Errorf("a log record left the trace: %s", line)
		}
	}

	// And the chain is intact, so the journey is one shape rather than four
	// spans that merely agree on an id.
	byName := map[string]telemetry.SpanRecord{}
	for _, s := range recorded {
		byName[s.Name] = s
	}
	for _, step := range []struct{ child, parent string }{
		{"handler", "rpc"},
		{"module.tmdb.Import", "handler"},
		{"sql INSERT nodes", "module.tmdb.Import"},
	} {
		if got, want := byName[step.child].ParentID, byName[step.parent].Trace.SpanIDString(); got != want {
			t.Errorf("%s parent is %q, want %s's span %q", step.child, got, step.parent, want)
		}
	}
	// The entry span hangs off the caller's own span, which is outside this
	// process and is why the trace continues rather than beginning here.
	if got := byName["rpc"].ParentID; got != caller.SpanIDString() {
		t.Errorf("the entry span parents to %q, want the caller's span %q", got, caller.SpanIDString())
	}

	// Each log record must carry the span it happened in, not an ancestor's.
	for name, want := range map[string]string{
		"handling":   byName["rpc"].Trace.SpanIDString(),
		"authorised": byName["handler"].Trace.SpanIDString(),
		"fetching":   byName["module.tmdb.Import"].Trace.SpanIDString(),
		"inserted":   byName["sql INSERT nodes"].Trace.SpanIDString(),
	} {
		for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
			if strings.Contains(line, `"message":"`+name+`"`) && !strings.Contains(line, `"span":"`+want+`"`) {
				t.Errorf("the %q record does not carry its own span %s: %s", name, want, line)
			}
		}
	}
}
