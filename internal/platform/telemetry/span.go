// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"context"
	"sync"
	"time"
)

// SpanRecord is one completed unit of work: what it was, how long it took, and
// where it sits in the trace. Spans answer the question logs cannot — a log
// line says a thing happened, a span says how long it took and what it
// contained — which is what turns "the page took nine seconds" into "the
// aggregator call took eight of them".
type SpanRecord struct {
	Trace     TraceContext
	ParentID  string
	Name      string
	Component string
	Module    string
	Start     time.Time
	End       time.Time
	// Status is "ok" or "error". A closed vocabulary, so the store can index
	// it and the viewer can colour by it without parsing prose.
	Status string
	// ErrorCategory is the Platform error category when the span failed
	// (contracts.Internal, contracts.NotFound, …). The seven categories are the
	// Platform's own closed vocabulary and are safe verbatim.
	ErrorCategory string
	Attributes    []Field
	Resource      Resource
}

// Duration is how long the span took. Zero for a span that never ended.
func (s SpanRecord) Duration() time.Duration {
	if s.End.IsZero() {
		return 0
	}
	return s.End.Sub(s.Start)
}

// SpanSink receives completed spans. Like Sink it must be safe for concurrent
// use and must never panic.
type SpanSink interface {
	WriteSpan(SpanRecord)
}

// discardSpanSink drops everything, backing an unconfigured context.
type discardSpanSink struct{}

func (discardSpanSink) WriteSpan(SpanRecord) {}

// spanSinkKey carries the sink through the context, so the composition root
// configures it once and no layer takes it as a parameter — the same ambient
// rule the logger follows (ADR 0053).
type spanSinkKey struct{}

// WithSpanSink returns a context whose spans are written to sink.
func WithSpanSink(ctx context.Context, sink SpanSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, spanSinkKey{}, sink)
}

// spanSinkFrom returns the configured sink, or one that discards.
func spanSinkFrom(ctx context.Context) SpanSink {
	if ctx == nil {
		return discardSpanSink{}
	}
	if s, ok := ctx.Value(spanSinkKey{}).(SpanSink); ok && s != nil {
		return s
	}
	return discardSpanSink{}
}

// Span is an in-flight unit of work. It is created by Start and completed by
// End, which is the only point at which anything is written — an unended span
// is simply never recorded, so a panicking path costs a missing span rather
// than a corrupt trace.
type Span struct {
	mu     sync.Mutex
	record SpanRecord
	sink   SpanSink
	ended  bool
}

// Start begins a span named name and returns a context carrying it as the
// current parent.
//
// The returned context is what makes nesting work: a span started from it
// becomes this span's child, so the seams compose into a tree without any of
// them knowing about each other. A caller that ignores the returned context
// still gets a correct span — it just will not parent anything.
//
// It is always safe to call. With no trace in ctx it mints one, and with no
// sink configured the span is built and discarded, so a unit test exercising
// an instrumented path needs no telemetry setup at all.
func Start(ctx context.Context, name string, attrs ...Field) (context.Context, *Span) {
	parent, ok := TraceFrom(ctx)
	if !ok || parent.TraceID == [16]byte{} {
		// No trace at all: this span begins one, and is its root.
		parent = NewRootTrace()
	}
	current := parent.Child()

	lg := From(ctx)
	span := &Span{
		record: SpanRecord{
			Trace: current,
			// Empty for a root span. SpanIDString already renders a zero span
			// id as "", so a trace started here records no parent rather than
			// a run of zeros that looks like a real id.
			ParentID:  parent.SpanIDString(),
			Name:      name,
			Component: lg.component,
			Module:    lg.module,
			Start:     time.Now(),
			Status:    "ok",
			// Copied, not aliased: a caller reusing its attrs slice after
			// Start must not be able to mutate a span already in flight.
			Attributes: append([]Field(nil), attrs...),
			Resource:   lg.resource,
		},
		sink: spanSinkFrom(ctx),
	}

	// Rebind both the trace and the logger, so log records emitted inside this
	// span carry *its* span id rather than its parent's. Without this a log
	// line and the span it happened in would agree on the trace and disagree
	// on where in it, which is worse than having no span id at all.
	ctx = TraceInto(ctx, current)
	ctx = Into(ctx, lg.WithTrace(current))
	return ctx, span
}

// SetAttributes adds attributes to a span in flight. They are redaction-classed
// like any other field, so an attribute is subject to exactly the same rules as
// a log field (ADR 0056) rather than being a second, laxer channel.
func (s *Span) SetAttributes(attrs ...Field) {
	if s == nil || len(attrs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.record.Attributes = append(s.record.Attributes, attrs...)
}

// Fail marks the span as failed, recording the Platform error category.
//
// category is the caller's, because only the caller knows it: the seven
// categories are a Platform contract and this package must not try to infer
// one from an error's text. An empty category is fine — the span is still
// marked failed, which is the part that matters for finding it.
func (s *Span) Fail(category string, err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.record.Status = "error"
	s.record.ErrorCategory = category
	s.record.Attributes = append(s.record.Attributes, Err(err))
}

// End completes the span and writes it. It is idempotent, so `defer span.End()`
// alongside an explicit End on an error path is safe rather than a double
// write.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.record.End = time.Now()
	record := s.record
	sink := s.sink
	s.mu.Unlock()

	sink.WriteSpan(record)
}

// TraceContext is the span's own context — its trace, and its span id as the
// parent for anything nested inside it.
func (s *Span) TraceContext() TraceContext {
	if s == nil {
		return TraceContext{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.record.Trace
}
