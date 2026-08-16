// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// The bridge between Mosaic's TraceContext and OpenTelemetry's SpanContext
// (sdk#8).
//
// The trace id is Mosaic's correlation id (platform#32): it is on every log
// record, every span, every event and every outbox row, and it is what joins a
// support report to the logs. A conversion producing a trace id rather than
// the same trace id would pass any test asserting one exists and would
// silently sever that join, so the mapping here is field for field with
// nothing invented and nothing dropped.
//
// That the two representations are the same three values is not luck:
// platform#32 chose W3C Trace Context so that an OTLP export and off-the-shelf
// instrumentation need no translation layer at each boundary.
//
// The bridge is tested in both directions and round-trip.

// SpanContext renders tc as OpenTelemetry's equivalent.
//
// The trace flags carry the sampling decision and nothing else, matching what
// Traceparent already writes on the wire. Remote is deliberately not set: this
// is used for a context this process holds, and OTel's own propagator sets the
// remote flag when it extracts one from a header.
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
// A zero span id must survive the round trip. NewRootTrace mints a trace whose
// span id is deliberately zero, meaning "nothing precedes this", so a root
// trace must not come back with an invented parent. OTel's SpanContext holds
// the zero span id happily; what it will not do is report itself IsValid,
// which is why this reads the fields directly rather than gating on validity.
func TraceContextOf(sc trace.SpanContext) TraceContext {
	return TraceContext{
		TraceID: [16]byte(sc.TraceID()),
		SpanID:  [8]byte(sc.SpanID()),
		Sampled: sc.IsSampled(),
	}
}

// The tracer, and the exporter that carries a finished span back to Mosaic's
// SpanSink (sdk#8).
//
// The sink is what keeps the conversion contained. Spans are produced by
// OpenTelemetry, so off-the-shelf instrumentation composes with Mosaic's own,
// but a finished span is handed to the same SpanSink the PostgreSQL store and
// the expert-mode viewer already read — neither the schema nor the surface a
// person looks at depends on the representation.
//
// Sampling is AlwaysSample on purpose. platform#32 says the sampling decision
// governs whether spans are recorded and never whether the ids exist, but this
// implementation writes every span to the sink regardless of the flag, so a
// parent-based sampler here would silently stop recording spans for unsampled
// traces. Changing that is a decision about retention, not about the
// representation.

// The reserved attribute keys a span carries so the exporter can rebuild a
// SpanRecord. They are Mosaic's own dimensions, which OpenTelemetry has no
// convention for: a component and a module are who is speaking, and the error
// category is one of the Platform's seven (platform#34's vocabulary, not
// OTel's).
const (
	attrComponent     = "mosaic.component"
	attrModule        = "mosaic.module"
	attrErrorCategory = "mosaic.error_category"
	// The process identity, in OpenTelemetry's conventional keys where it has
	// one. It rides on the span rather than on the provider because the
	// identity comes from the ambient logger at the moment the span starts,
	// and the provider is built once by whoever configured the sink. Moving it
	// onto the provider is the intended end state and is not built.
	attrServiceName     = "service.name"
	attrServiceVersion  = "service.version"
	attrServiceInstance = "service.instance.id"
	attrGenerationID    = "mosaic.generation.id"
	attrBootID          = "mosaic.boot.id"
)

// tracerKey carries the tracer beside the sink, so the ambient rule the rest of
// this package follows (platform#31) is unchanged: the composition root configures
// it once, and no layer takes it as a parameter.
type tracerKey struct{}

// tracerFrom returns the configured tracer, or one whose spans go nowhere.
//
// A span is produced either way, which is what keeps Start safe to call with
// no telemetry set up at all: a unit test exercising an instrumented path gets
// real ids and a discarded span rather than a nil check at every seam.
func tracerFrom(ctx context.Context) trace.Tracer {
	if ctx != nil {
		if t, ok := ctx.Value(tracerKey{}).(trace.Tracer); ok && t != nil {
			return t
		}
	}
	return discardTracerProvider().Tracer(tracerName)
}

// tracerName identifies this instrumentation scope in an exported span.
const tracerName = "github.com/mosaic-media/platform"

// discardTracer is built once: a provider with no processor still mints valid
// span contexts, which is exactly the "ids without a destination" an unconfigured
// context needs.
var (
	discardOnce     sync.Once
	discardProvider *sdktrace.TracerProvider
)

func discardTracerProvider() *sdktrace.TracerProvider {
	discardOnce.Do(func() {
		discardProvider = sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()))
	})
	return discardProvider
}

// newTracerProvider builds a provider whose finished spans reach sink.
//
// SimpleSpanProcessor rather than batching: the sink is the Platform's own, and
// it already batches on its way to PostgreSQL. Adding a second queue here would
// mean two places a span can be dropped and two places to look when one is.
func newTracerProvider(sink SpanSink) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(&sinkExporter{sink: sink})),
	)
}

// sinkExporter converts a finished OpenTelemetry span into a SpanRecord.
type sinkExporter struct{ sink SpanSink }

func (e *sinkExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		e.sink.WriteSpan(spanRecordOf(span))
	}
	return nil
}

func (e *sinkExporter) Shutdown(context.Context) error { return nil }

// spanRecordOf rebuilds Mosaic's record from an OpenTelemetry span.
func spanRecordOf(span sdktrace.ReadOnlySpan) SpanRecord {
	record := SpanRecord{
		Trace:  TraceContextOf(span.SpanContext()),
		Name:   span.Name(),
		Start:  span.StartTime(),
		End:    span.EndTime(),
		Status: "ok",
	}
	// A root span's parent is absent rather than zero, which is the same thing
	// SpanIDString renders as empty — so a trace started here records no parent
	// rather than a run of zeros that looks like a real id.
	if parent := span.Parent(); parent.HasSpanID() {
		record.ParentID = parent.SpanID().String()
	}
	if span.Status().Code == codes.Error {
		record.Status = "error"
	}

	for _, kv := range span.Attributes() {
		switch string(kv.Key) {
		case attrComponent:
			record.Component = kv.Value.AsString()
		case attrModule:
			record.Module = kv.Value.AsString()
		case attrErrorCategory:
			record.ErrorCategory = kv.Value.AsString()
		case attrServiceName:
			record.Resource.ServiceName = kv.Value.AsString()
		case attrServiceVersion:
			record.Resource.ServiceVersion = kv.Value.AsString()
		case attrServiceInstance:
			record.Resource.InstanceID = kv.Value.AsString()
		case attrGenerationID:
			record.Resource.GenerationID = kv.Value.AsString()
		case attrBootID:
			record.Resource.BootID = kv.Value.AsString()
		default:
			// Everything a caller set, carried back verbatim. It has already
			// been through redaction on the way in (platform#34), so this is a
			// re-presentation rather than a second chance to classify.
			record.Attributes = append(record.Attributes, String(string(kv.Key), kv.Value.Emit()))
		}
	}
	return record
}
