// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

func TestTraceparentRoundTrips(t *testing.T) {
	tc := telemetry.NewTraceContext()
	parsed, ok := telemetry.ParseTraceparent(tc.Traceparent())
	if !ok {
		t.Fatalf("a freshly minted traceparent must parse: %q", tc.Traceparent())
	}
	if parsed.TraceID != tc.TraceID || parsed.SpanID != tc.SpanID || parsed.Sampled != tc.Sampled {
		t.Fatalf("round trip lost information: %+v vs %+v", parsed, tc)
	}
}

// TestParseTraceparentRejectsMalformedInput matters more than the happy path:
// the header is attacker-controlled, so anything not exactly right must be
// discarded outright rather than repaired or partially accepted (ADR 0054).
func TestParseTraceparentRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"too few parts":    "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331",
		"too many parts":   "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01-extra",
		"short trace id":   "00-0af7651916cd43dd-b7ad6b7169203331-01",
		"short span id":    "00-0af7651916cd43dd8448eb211c80319c-b7ad6b71-01",
		"non-hex trace id": "00-zzf7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"forbidden ff":     "ff-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"zero trace id":    "00-00000000000000000000000000000000-b7ad6b7169203331-01",
		"zero span id":     "00-0af7651916cd43dd8448eb211c80319c-0000000000000000-01",
		"sql-ish":          "00-' OR 1=1 --00000000000000000000-b7ad6b7169203331-01",
	}
	for name, value := range cases {
		if _, ok := telemetry.ParseTraceparent(value); ok {
			t.Errorf("%s: %q must be rejected", name, value)
		}
	}
}

// TestParseTraceparentAcceptsAFutureVersion guards a subtle failure: rejecting
// an unknown version would silently break correlation with a newer caller,
// which is worse than accepting a header whose extra fields we ignore.
func TestParseTraceparentAcceptsAFutureVersion(t *testing.T) {
	if _, ok := telemetry.ParseTraceparent("01-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"); !ok {
		t.Fatal("a future version must still correlate")
	}
}

func TestChildKeepsTheTraceAndChangesTheSpan(t *testing.T) {
	parent := telemetry.NewTraceContext()
	child := parent.Child()
	if child.TraceID != parent.TraceID {
		t.Fatal("a child must stay in the same trace")
	}
	if child.SpanID == parent.SpanID {
		t.Fatal("a child must get its own span")
	}
}

// TestStartRequestContinuesAnInboundTrace is the property the whole
// cross-repository story rests on: the Shell's id must survive into the
// Platform, not be replaced by a fresh one.
func TestStartRequestContinuesAnInboundTrace(t *testing.T) {
	upstream := telemetry.NewTraceContext()

	ctx := telemetry.StartRequest(context.Background(), upstream.Traceparent())
	got, ok := telemetry.TraceFrom(ctx)
	if !ok {
		t.Fatal("StartRequest must seed a trace context")
	}
	if got.TraceID != upstream.TraceID {
		t.Fatalf("trace id changed across the edge: %s != %s", got.TraceIDString(), upstream.TraceIDString())
	}
	// The caller's span id is carried through deliberately: it is the parent
	// the first Start will attach to. This process's own span is created by
	// Start, not here — creating one here too is what produced a parent naming
	// no span (see TestEntrySpanParentsToTheCallersOwnSpan).
	if got.SpanID != upstream.SpanID {
		t.Fatalf("StartRequest must carry the caller's span as the parent-to-be, got %s want %s",
			got.SpanIDString(), upstream.SpanIDString())
	}
}

func TestStartRequestMintsWhenTheHeaderIsAbsentOrJunk(t *testing.T) {
	for _, header := range []string{"", "not-a-traceparent"} {
		ctx := telemetry.StartRequest(context.Background(), header)
		tc, ok := telemetry.TraceFrom(ctx)
		if !ok || tc.TraceIDString() == "" {
			t.Fatalf("header %q: a malformed or absent header must start a fresh trace, not leave none", header)
		}
		// ...and that fresh trace has no span yet, so the first span becomes a
		// true root rather than hanging off an id nothing ever recorded.
		if tc.SpanIDString() != "" {
			t.Fatalf("header %q: a fresh trace must have no parent span, got %s", header, tc.SpanIDString())
		}
	}
}

// TestStartRequestBindsTheTraceToTheLogger closes the loop: seeding a trace is
// only useful if records emitted downstream actually carry it without anyone
// naming it.
func TestStartRequestBindsTheTraceToTheLogger(t *testing.T) {
	var buf bytes.Buffer
	base := telemetry.Into(context.Background(),
		telemetry.New(telemetry.NewJSONSink(&buf), telemetry.Resource{}, telemetry.LevelDebug))

	upstream := telemetry.NewTraceContext()
	ctx := telemetry.StartRequest(base, upstream.Traceparent())
	telemetry.From(ctx).Info("did some work")

	line := parseLogLine(t, buf.Bytes())
	if line["trace"] != upstream.TraceIDString() {
		t.Fatalf("trace = %v, want %s", line["trace"], upstream.TraceIDString())
	}
	if line["span"] == "" || line["span"] == nil {
		t.Fatal("expected a span id on the record")
	}
}

// TestUnsampledTraceStillCarriesItsID is ADR 0054's sampling rule: a sampling
// decision governs whether spans are recorded, never whether the id exists —
// otherwise an unsampled failure is unjoinable to its own logs.
func TestUnsampledTraceStillCarriesItsID(t *testing.T) {
	var buf bytes.Buffer
	base := telemetry.Into(context.Background(),
		telemetry.New(telemetry.NewJSONSink(&buf), telemetry.Resource{}, telemetry.LevelDebug))

	upstream := telemetry.NewTraceContext()
	upstream.Sampled = false

	ctx := telemetry.StartRequest(base, upstream.Traceparent())
	tc, _ := telemetry.TraceFrom(ctx)
	if tc.Sampled {
		t.Fatal("the sampling decision must be carried, not overridden")
	}
	telemetry.From(ctx).Info("unsampled but recorded")
	if line := parseLogLine(t, buf.Bytes()); line["trace"] != upstream.TraceIDString() {
		t.Fatalf("an unsampled trace must still stamp its id, got %v", line["trace"])
	}
}

func TestHTTPMiddlewareSeedsAndEchoesTheTrace(t *testing.T) {
	var seen string
	h := telemetry.HTTPMiddleware("artwork", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc, _ := telemetry.TraceFrom(r.Context())
		seen = tc.TraceIDString()
		w.WriteHeader(http.StatusOK)
	}))

	upstream := telemetry.NewTraceContext()
	req := httptest.NewRequest(http.MethodGet, "/artwork", nil)
	req.Header.Set(telemetry.TraceparentHeader, upstream.Traceparent())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seen != upstream.TraceIDString() {
		t.Fatalf("handler saw trace %q, want %q", seen, upstream.TraceIDString())
	}
	// Echoed back so a bug report arrives with the one string that makes it
	// reconstructible, even when the reporter is a browser network tab.
	if got := rec.Header().Get(telemetry.TraceIDHeader); got != upstream.TraceIDString() {
		t.Fatalf("response header = %q, want %q", got, upstream.TraceIDString())
	}
}

func TestHTTPMiddlewareStartsATraceWithoutAHeader(t *testing.T) {
	h := telemetry.HTTPMiddleware("handoff", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	got := rec.Header().Get(telemetry.TraceIDHeader)
	if len(got) != 32 || strings.Trim(got, "0") == "" {
		t.Fatalf("expected a freshly minted trace id, got %q", got)
	}
}

// TestEntrySpanParentsToTheCallersOwnSpan is a regression test for a defect
// only a real trace showed: the tree was unrootable.
//
// StartRequest used to Child() the inbound context, and Start Child()s again —
// so the first span's parent was an id no span ever had. Every trace looked
// like it descended from something missing, and nothing linked the request
// back to the caller's span across the wire. Both halves of a
// cross-repository trace were present and could not be joined.
func TestEntrySpanParentsToTheCallersOwnSpan(t *testing.T) {
	caller := telemetry.NewTraceContext()

	ctx := telemetry.StartRequest(context.Background(), caller.Traceparent())
	sink := &captureSpans{}
	ctx = telemetry.WithSpanSink(ctx, sink)

	_, span := telemetry.Start(ctx, "rpc")
	span.End()

	got, ok := sink.byName("rpc")
	if !ok {
		t.Fatal("no span recorded")
	}
	if got.Trace.TraceIDString() != caller.TraceIDString() {
		t.Fatalf("entry span left the caller's trace: %s", got.Trace.TraceIDString())
	}
	if got.ParentID != caller.SpanIDString() {
		t.Fatalf("entry span parent = %q, want the caller's span %q — a parent naming no span makes the tree unrootable",
			got.ParentID, caller.SpanIDString())
	}
}

// TestAFreshTraceHasATrulyParentlessRoot is the other half: with no inbound
// header there is no caller span, so the root must record no parent at all
// rather than a minted id that names nothing.
func TestAFreshTraceHasATrulyParentlessRoot(t *testing.T) {
	ctx := telemetry.StartRequest(context.Background(), "")
	sink := &captureSpans{}
	ctx = telemetry.WithSpanSink(ctx, sink)

	ctx, root := telemetry.Start(ctx, "root")
	_, child := telemetry.Start(ctx, "child")
	child.End()
	root.End()

	gotRoot, _ := sink.byName("root")
	gotChild, _ := sink.byName("child")

	if gotRoot.ParentID != "" {
		t.Fatalf("root span parent = %q, want empty", gotRoot.ParentID)
	}
	if gotChild.ParentID != gotRoot.Trace.SpanIDString() {
		t.Fatalf("child parent = %q, want the root span %q", gotChild.ParentID, gotRoot.Trace.SpanIDString())
	}
}

// TestEveryParentIDNamesARealSpan is the property both cases above serve, and
// the one a trace viewer actually depends on: within a trace, every non-empty
// parent must resolve to a span that exists.
func TestEveryParentIDNamesARealSpan(t *testing.T) {
	caller := telemetry.NewTraceContext()
	ctx := telemetry.StartRequest(context.Background(), caller.Traceparent())
	sink := &captureSpans{}
	ctx = telemetry.WithSpanSink(ctx, sink)

	rpcCtx, rpc := telemetry.Start(ctx, "rpc")
	txCtx, tx := telemetry.Start(rpcCtx, "tx")
	_, sql := telemetry.Start(txCtx, "sql")
	sql.End()
	tx.End()
	rpc.End()

	ids := map[string]bool{}
	for _, s := range sink.all() {
		ids[s.Trace.SpanIDString()] = true
	}
	// The caller's span is legitimately outside this process.
	ids[caller.SpanIDString()] = true

	for _, s := range sink.all() {
		if s.ParentID != "" && !ids[s.ParentID] {
			t.Fatalf("span %q has parent %q, which names no span in the trace", s.Name, s.ParentID)
		}
	}
}
