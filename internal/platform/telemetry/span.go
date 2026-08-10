// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
// rule the logger follows (platform#31).
type spanSinkKey struct{}

// WithSpanSink returns a context whose spans are written to sink.
func WithSpanSink(ctx context.Context, sink SpanSink) context.Context {
	if sink == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, spanSinkKey{}, sink)
	// The tracer travels with the sink rather than being configured separately,
	// so there is exactly one thing to set up and no way to have a tracer whose
	// spans go somewhere other than the sink beside it (sdk#8).
	return context.WithValue(ctx, tracerKey{}, newTracerProvider(sink).Tracer(tracerName))
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
	mu    sync.Mutex
	span  trace.Span
	trace TraceContext
	ended bool
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
	// The parent comes from Mosaic's own trace context, which StartRequest and
	// TraceInto both seed — and TraceInto seeds OpenTelemetry's alongside it, so
	// the tracer below parents to the same span this reads. A trace with no
	// parent at all begins one here, and is its root.
	parent, ok := TraceFrom(ctx)
	if !ok || parent.TraceID == [16]byte{} {
		ctx = TraceInto(ctx, NewRootTrace())
	}

	lg := From(ctx)
	// Set at start rather than on the provider: the identity is the ambient
	// logger's, and the exporter reads them back to rebuild a SpanRecord.
	started := append(resourceAttributes(lg.resource),
		attribute.String(attrComponent, lg.component),
		attribute.String(attrModule, lg.module))
	for _, f := range attrs {
		started = append(started, attributeOf(f))
	}

	ctx, otelSpan := tracerFrom(ctx).Start(ctx, name, trace.WithAttributes(started...))
	current := TraceContextOf(otelSpan.SpanContext())
	span := &Span{span: otelSpan, trace: current}

	// Rebind both the trace and the logger, so log records emitted inside this
	// span carry *its* span id rather than its parent's. Without this a log
	// line and the span it happened in would agree on the trace and disagree
	// on where in it, which is worse than having no span id at all.
	ctx = TraceInto(ctx, current)
	ctx = Into(ctx, lg.WithTrace(current))
	return ctx, span
}

// attributeOf renders a Field as an OpenTelemetry attribute, applying redaction
// on the way out exactly as a sink does — a span attribute is not a laxer
// channel than a log field (platform#34).
func attributeOf(f Field) attribute.KeyValue {
	value := f.EmitValue()
	switch v := value.(type) {
	case string:
		return attribute.String(f.Key, v)
	case bool:
		return attribute.Bool(f.Key, v)
	case int:
		return attribute.Int(f.Key, v)
	case int64:
		return attribute.Int64(f.Key, v)
	case float64:
		return attribute.Float64(f.Key, v)
	case nil:
		return attribute.String(f.Key, "")
	default:
		return attribute.String(f.Key, fmt.Sprint(v))
	}
}

// resourceAttributes renders the process identity for a span to carry.
func resourceAttributes(r Resource) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(attrServiceName, r.ServiceName),
		attribute.String(attrServiceVersion, r.ServiceVersion),
		attribute.String(attrServiceInstance, r.InstanceID),
		attribute.String(attrGenerationID, r.GenerationID),
		attribute.String(attrBootID, r.BootID),
	}
}

// SetAttributes adds attributes to a span in flight. They are redaction-classed
// like any other field, so an attribute is subject to exactly the same rules as
// a log field (platform#34) rather than being a second, laxer channel.
func (s *Span) SetAttributes(attrs ...Field) {
	if s == nil || len(attrs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	for _, f := range attrs {
		s.span.SetAttributes(attributeOf(f))
	}
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
	s.span.SetStatus(codes.Error, err.Error())
	s.span.SetAttributes(attribute.String(attrErrorCategory, category))
	s.span.SetAttributes(attributeOf(Err(err)))
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
	span := s.span
	s.mu.Unlock()

	// The exporter behind the tracer is what turns this into a SpanRecord and
	// hands it to the sink, so ending is the only write and it happens once.
	span.End()
}

// TraceContext is the span's own context — its trace, and its span id as the
// parent for anything nested inside it.
func (s *Span) TraceContext() TraceContext {
	if s == nil {
		return TraceContext{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trace
}
