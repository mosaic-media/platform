// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// TraceparentHeader is the W3C Trace Context header every Mosaic edge reads
// and writes. Using the standard rather than a bespoke header is what makes a
// future OTLP export, and any off-the-shelf instrumentation, work without a
// translation layer at each boundary (ADR 0054).
const TraceparentHeader = "traceparent"

// TraceContext identifies one distributed operation and this process's place
// in it. The trace id *is* Mosaic's correlation id — there is no second
// identifier, because two would mean two things to propagate, two to store and
// exactly one that gets forgotten at some new edge (ADR 0054).
type TraceContext struct {
	// TraceID is stable for the whole operation: a Shell click, the session
	// intent it sends, the command handler, the module, the SQL, and the
	// outbox row committed alongside it all carry this one value.
	TraceID [16]byte
	// SpanID identifies this process's unit of work within the trace.
	SpanID [8]byte
	// Sampled carries the upstream sampling decision. It governs whether spans
	// are *recorded*; it never governs whether the ids exist. A log line and an
	// event row carry the trace id even in an unsampled trace, so a support
	// report is always joinable to the logs.
	Sampled bool
}

// NewTraceContext mints a fresh, sampled trace with a span id of its own.
//
// Prefer NewRootTrace at an edge. This is for callers that need a complete,
// immediately propagatable context — tests, and anything formatting a
// traceparent before it has started a span.
func NewTraceContext() TraceContext {
	tc := TraceContext{Sampled: true}
	_, _ = rand.Read(tc.TraceID[:])
	_, _ = rand.Read(tc.SpanID[:])
	return tc
}

// NewRootTrace mints a fresh trace whose span id is deliberately **zero**.
//
// A TraceContext in a context means "the trace, and the span anything started
// here should hang off". At the start of a brand-new trace there is no such
// span yet — so a minted span id would name nothing, and the first real span
// would record a parent that does not exist. That produces a tree a viewer
// cannot root: it holds a parent id matching no span.
//
// A zero span id says exactly that: nothing precedes this. Start turns it into
// an empty ParentID, which is what a root span should carry.
func NewRootTrace() TraceContext {
	tc := TraceContext{Sampled: true}
	_, _ = rand.Read(tc.TraceID[:])
	return tc
}

// Child returns tc with a fresh span id, keeping the trace and the sampling
// decision. This is what an edge does with an inbound traceparent: the caller's
// span becomes this span's parent, and the trace continues rather than
// restarting.
func (tc TraceContext) Child() TraceContext {
	child := tc
	_, _ = rand.Read(child.SpanID[:])
	return child
}

// Valid reports whether tc carries usable ids. The W3C spec makes an all-zero
// trace or span id invalid, so a caller that sends one is treated as having
// sent nothing.
func (tc TraceContext) Valid() bool {
	return tc.TraceID != [16]byte{} && tc.SpanID != [8]byte{}
}

// TraceIDString renders the trace id — the value that appears on every log
// record, every event envelope and every audit row, and the one string a user
// pastes into a bug report.
func (tc TraceContext) TraceIDString() string {
	if tc.TraceID == [16]byte{} {
		return ""
	}
	return hex.EncodeToString(tc.TraceID[:])
}

// SpanIDString renders the span id.
func (tc TraceContext) SpanIDString() string {
	if tc.SpanID == [8]byte{} {
		return ""
	}
	return hex.EncodeToString(tc.SpanID[:])
}

// Traceparent formats tc as a W3C traceparent header value, for propagation to
// a downstream service or a third-party HTTP call.
func (tc TraceContext) Traceparent() string {
	if !tc.Valid() {
		return ""
	}
	flags := "00"
	if tc.Sampled {
		flags = "01"
	}
	return "00-" + hex.EncodeToString(tc.TraceID[:]) + "-" + hex.EncodeToString(tc.SpanID[:]) + "-" + flags
}

// ParseTraceparent parses an inbound header value.
//
// The value is untrusted input: a client controls it, so a client can forge
// it. It is parsed strictly and discarded outright if malformed — never
// repaired, never partially accepted — and it is used for correlation only.
// Nothing downstream may route, authorise or rate-limit on it (ADR 0054).
func ParseTraceparent(value string) (TraceContext, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 {
		return TraceContext{}, false
	}
	version, traceID, spanID, flags := parts[0], parts[1], parts[2], parts[3]

	// Version ff is forbidden by the spec. Any other version is accepted:
	// a future version must remain parseable by this field layout, and
	// rejecting one would silently break correlation with a newer caller.
	if len(version) != 2 || version == "ff" {
		return TraceContext{}, false
	}
	if len(traceID) != 32 || len(spanID) != 16 || len(flags) != 2 {
		return TraceContext{}, false
	}

	var tc TraceContext
	trace, err := hex.DecodeString(traceID)
	if err != nil {
		return TraceContext{}, false
	}
	span, err := hex.DecodeString(spanID)
	if err != nil {
		return TraceContext{}, false
	}
	flagBytes, err := hex.DecodeString(flags)
	if err != nil {
		return TraceContext{}, false
	}
	copy(tc.TraceID[:], trace)
	copy(tc.SpanID[:], span)
	tc.Sampled = flagBytes[0]&0x01 == 1

	if !tc.Valid() {
		return TraceContext{}, false
	}
	return tc, true
}

// traceKey is unexported so nothing outside this package can plant a trace
// context by constructing the key.
type traceKey struct{}

// TraceInto returns a context carrying tc.
func TraceInto(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceKey{}, tc)
}

// TraceFrom returns the TraceContext ctx carries, and whether it carried one.
// Callers that only want the id can ignore the boolean: a zero TraceContext
// renders as an empty string rather than a misleading run of zeros.
func TraceFrom(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	tc, ok := ctx.Value(traceKey{}).(TraceContext)
	return tc, ok
}

// StartRequest is the one call every edge makes. It continues the caller's
// trace when the header is well-formed and starts a fresh one otherwise,
// creates this process's span within it, and seeds both the trace context and
// a logger already bound to it — so every record emitted downstream carries
// the trace without any code below naming it.
//
// A malformed or absent header is not an error and is never reported as one.
// It simply means this process is the start of the trace.
func StartRequest(ctx context.Context, traceparent string) context.Context {
	tc, ok := ParseTraceparent(traceparent)
	if !ok {
		tc = NewRootTrace()
	}
	// The inbound context is carried **as parsed**, with the caller's span id
	// intact — it is not Child()ed here.
	//
	// It used to be, and the effect was only visible in a real trace: Child()
	// here plus the Child() inside Start meant the first span's parent was an
	// id no span ever had. The request appeared to descend from something
	// missing, and nothing linked it back to the caller's span across the wire.
	// The first Start is what creates this process's span; this call's job is
	// only to say which trace it belongs to and what it hangs off.
	ctx = TraceInto(ctx, tc)
	return Into(ctx, From(ctx).WithTrace(tc))
}
