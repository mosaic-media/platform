// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"context"

	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	logsdk "go.opentelemetry.io/otel/sdk/log"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// Log records become OpenTelemetry's (sdk#8), and the Sink stays.
//
// The same containment as the span half: a record is *produced* as an
// OpenTelemetry record, and a finished one is handed to the same `Sink` the
// JSON file, the console and the PostgreSQL store already read. Nothing about
// the wire shape, the schema or the expert-mode viewer moves in this change —
// what moves is what a record *is* on the way there, so an OTLP exporter can sit
// beside the sink later without touching a single one of the Platform's 324
// classified call sites.
//
// **The trace and span ids travel in the emit context**, which is how
// OpenTelemetry correlates a record with a span, rather than as two more
// attributes. That is what keeps a journey coherent across the conversion: the
// ids on a log record and the ids on the span it happened inside are the same
// values arriving by the same route (platform#32).

// logRecordExporter rebuilds Mosaic's Record from an OpenTelemetry one and
// writes it to the sink.
type logRecordExporter struct{ sink Sink }

func (e *logRecordExporter) Export(_ context.Context, records []logsdk.Record) error {
	for i := range records {
		e.sink.Write(recordOf(&records[i]))
	}
	return nil
}

func (e *logRecordExporter) Shutdown(context.Context) error   { return nil }
func (e *logRecordExporter) ForceFlush(context.Context) error { return nil }

// newLoggerProvider builds a provider whose records reach sink.
//
// SimpleProcessor rather than batching, for the reason the span half uses one:
// the Platform's own sink already batches on its way to PostgreSQL, and a second
// queue here would mean two places a record can be dropped and two to look in
// when one is.
func newLoggerProvider(sink Sink) *logsdk.LoggerProvider {
	return logsdk.NewLoggerProvider(
		logsdk.WithProcessor(logsdk.NewSimpleProcessor(&logRecordExporter{sink: sink})),
	)
}

// recordOf rebuilds a Record from an OpenTelemetry log record.
func recordOf(record *logsdk.Record) Record {
	out := Record{
		Time:    record.Timestamp(),
		Level:   levelOf(record.Severity()),
		Message: record.Body().AsString(),
		Trace: TraceContext{
			TraceID: [16]byte(record.TraceID()),
			SpanID:  [8]byte(record.SpanID()),
			Sampled: record.TraceFlags().IsSampled(),
		},
	}
	record.WalkAttributes(func(kv attribute.KeyValue) bool {
		switch string(kv.Key) {
		case attrComponent:
			out.Component = kv.Value.AsString()
		case attrModule:
			out.Module = kv.Value.AsString()
		case attrServiceName:
			out.Resource.ServiceName = kv.Value.AsString()
		case attrServiceVersion:
			out.Resource.ServiceVersion = kv.Value.AsString()
		case attrServiceInstance:
			out.Resource.InstanceID = kv.Value.AsString()
		case attrGenerationID:
			out.Resource.GenerationID = kv.Value.AsString()
		case attrBootID:
			out.Resource.BootID = kv.Value.AsString()
		default:
			out.Fields = append(out.Fields, fieldOf(kv))
		}
		return true
	})
	return out
}

// fieldOf restores a Field from an OpenTelemetry attribute, **keeping its
// type**.
//
// The type matters and is easy to lose. A count rendered back as text turns
// `"results":7` into `"results":"7"` in the JSON sink and in the telemetry
// store, which is a silent change to what every reader of those records parses
// — and one no test asserting "the field is present" would catch.
//
// The class is RedactionNone because classification was applied on the way *in*
// (platform#34): the value here is already what a sink is allowed to see, so
// marking it none makes EmitValue a no-op rather than redacting a second time.
func fieldOf(kv attribute.KeyValue) Field {
	field := Field{Key: string(kv.Key), Redaction: domain.RedactionNone}
	switch kv.Value.Type() {
	case attribute.BOOL:
		field.Value = kv.Value.AsBool()
	case attribute.INT64:
		field.Value = kv.Value.AsInt64()
	case attribute.FLOAT64:
		field.Value = kv.Value.AsFloat64()
	default:
		field.Value = kv.Value.AsString()
	}
	return field
}

// logValueOf renders a Field as an OpenTelemetry log attribute, applying
// redaction on the way out exactly as a sink would.
func logValueOf(f Field) attribute.KeyValue {
	switch v := f.EmitValue().(type) {
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

// severityOf maps a Level onto OpenTelemetry's scale.
func severityOf(l Level) log.Severity {
	switch l {
	case LevelDebug:
		return log.SeverityDebug
	case LevelWarn:
		return log.SeverityWarn
	case LevelError:
		return log.SeverityError
	default:
		return log.SeverityInfo
	}
}

// levelOf maps OpenTelemetry's scale back. It reads ranges rather than exact
// values, so a record from an instrumentation library using SeverityWarn2 —
// which is a legitimate part of the scale — does not come back as info.
func levelOf(s log.Severity) Level {
	switch {
	case s >= log.SeverityError:
		return LevelError
	case s >= log.SeverityWarn:
		return LevelWarn
	case s >= log.SeverityInfo:
		return LevelInfo
	default:
		return LevelDebug
	}
}
