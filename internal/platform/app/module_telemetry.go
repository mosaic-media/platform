// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"sync/atomic"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// moduleRecordQuota bounds how many records one module invocation may emit.
//
// Third-party code must not be able to fill the telemetry store, stall a
// request, or drown another module's records. The bound is per invocation
// rather than per interval so that a chatty module degrades its own call and
// nothing else, and it is generous enough that no reasonable module will meet
// it — a module that does is doing something worth knowing about, which is why
// the overflow is itself recorded.
const moduleRecordQuota = 512

// moduleTelemetry implements the SDK's v1.Telemetry over the Platform's own
// logger and spans (ADR 0059).
//
// The Platform stamps attribution; the module cannot. Module id, trace context
// and component are fixed here at the invocation seam, so a module cannot
// claim Platform origin, cannot attribute a record to a different module, and
// cannot alter the trace it belongs to. Everything a module supplies is
// content, never identity.
type moduleTelemetry struct {
	logger   *telemetry.Logger
	moduleID string
	// emitted is shared with every span this telemetry produces, so one quota
	// covers the whole invocation rather than one per object.
	emitted *atomic.Int64
	// unbounded lifts the per-invocation record quota. It is set only for the
	// long-lived telemetry an out-of-process module holds for its whole life
	// (see NewModuleTelemetry): a per-invocation cap does not map onto a process
	// that outlives any one call.
	unbounded bool
}

// newModuleTelemetry builds the SDK-facing telemetry for one in-process module
// invocation, record-quota-bounded so a chatty module degrades only its own
// call.
func newModuleTelemetry(lg *telemetry.Logger, moduleID string) *moduleTelemetry {
	return &moduleTelemetry{logger: lg, moduleID: moduleID, emitted: &atomic.Int64{}}
}

// NewModuleTelemetry builds the long-lived v1.Telemetry an out-of-process module
// observes through for its whole life (ADR 0059), attributed to moduleID and
// forwarding to the Platform logger with the Platform's redaction applied. The
// composition root hands it to the extension host when it adopts a module.
//
// It is deliberately not record-quota-bounded: the per-invocation cap the
// in-process path uses does not map onto a long-lived process, and how a
// long-lived out-of-process module's telemetry should be bounded — a rate limit,
// a sampling policy — is ADR 0077's open question, named here rather than
// answered with a guessed cap.
func NewModuleTelemetry(lg *telemetry.Logger, moduleID string) v1.Telemetry {
	return &moduleTelemetry{logger: lg, moduleID: moduleID, emitted: &atomic.Int64{}, unbounded: true}
}

func (m *moduleTelemetry) Debug(message string, fields ...v1.Field) {
	m.emit(telemetry.LevelDebug, message, fields)
}

func (m *moduleTelemetry) Info(message string, fields ...v1.Field) {
	m.emit(telemetry.LevelInfo, message, fields)
}

func (m *moduleTelemetry) Warn(message string, fields ...v1.Field) {
	m.emit(telemetry.LevelWarn, message, fields)
}

func (m *moduleTelemetry) Error(message string, fields ...v1.Field) {
	m.emit(telemetry.LevelError, message, fields)
}

// emit applies quota then forwards to the Platform logger.
func (m *moduleTelemetry) emit(level telemetry.Level, message string, fields []v1.Field) {
	if !m.claim() {
		return
	}
	converted := convertFields(fields)
	switch level {
	case telemetry.LevelDebug:
		m.logger.Debug(message, converted...)
	case telemetry.LevelWarn:
		m.logger.Warn(message, converted...)
	case telemetry.LevelError:
		m.logger.Error(message, converted...)
	default:
		m.logger.Info(message, converted...)
	}
}

// claim reserves one record against the invocation's quota, reporting whether
// there was room. The record that announces exhaustion is emitted exactly once
// — at the boundary — because a module in a tight loop would otherwise turn
// the quota warning into the flood it exists to prevent.
func (m *moduleTelemetry) claim() bool {
	if m.unbounded {
		return true
	}
	n := m.emitted.Add(1)
	if n < moduleRecordQuota {
		return true
	}
	if n == moduleRecordQuota {
		m.logger.Warn("module telemetry quota exhausted; further records from this invocation are dropped",
			telemetry.String("module", m.moduleID),
			telemetry.Int("quota", moduleRecordQuota))
	}
	return false
}

// Span starts a span attributed to the module.
func (m *moduleTelemetry) Span(ctx context.Context, name string, attrs ...v1.Field) (context.Context, v1.Span) {
	if !m.claim() {
		// Over quota: hand back a span that records nothing but still returns
		// a usable context, so the module's own nesting is unaffected.
		return ctx, noopModuleSpan{}
	}
	// Prefixed, so a module cannot name a span in a way that reads as the
	// Platform's own work in a waterfall.
	ctx, span := telemetry.Start(ctx, "module."+m.moduleID+"."+name, convertFields(attrs)...)
	return ctx, &moduleSpanAdapter{span: span}
}

// moduleSpanAdapter narrows the Platform span to what the SDK exposes.
type moduleSpanAdapter struct{ span *telemetry.Span }

func (a *moduleSpanAdapter) SetAttributes(attrs ...v1.Field) {
	a.span.SetAttributes(convertFields(attrs)...)
}

// Fail records the error. No category is taken from the module: the seven
// categories are a Platform contract, and letting a module assert one would
// let it describe its own failure in the Platform's vocabulary.
func (a *moduleSpanAdapter) Fail(err error) { a.span.Fail("", err) }

func (a *moduleSpanAdapter) End() { a.span.End() }

type noopModuleSpan struct{}

func (noopModuleSpan) SetAttributes(...v1.Field) {}
func (noopModuleSpan) Fail(error)                {}
func (noopModuleSpan) End()                      {}

// convertFields maps SDK fields onto Platform fields, applying the Platform's
// redaction policy.
//
// This is the point where the two vocabularies meet, and it is deliberately
// the Platform that decides: an SDK field states what a value *is*, and the
// Platform decides what happens to it. In particular Identifier is digested
// here, because the install salt is the Platform's and must never cross into a
// module.
func convertFields(fields []v1.Field) []telemetry.Field {
	if len(fields) == 0 {
		return nil
	}
	out := make([]telemetry.Field, 0, len(fields))
	for _, f := range fields {
		switch f.Redaction {
		case v1.RedactionNone:
			out = append(out, telemetry.Field{
				Key: f.Key, Value: f.Value, Redaction: domain.RedactionNone,
			})
		case v1.RedactionIdentifier:
			out = append(out, telemetry.Identifier(f.Key, f.Value))
		case v1.RedactionSecret:
			out = append(out, telemetry.Secret(f.Key, f.Value))
		default:
			// Sensitive, and — importantly — anything unrecognised, including
			// the zero value of a struct literal a module built by hand. Fail
			// closed on the way in as well as on the way out.
			out = append(out, telemetry.Sensitive(f.Key, f.Value))
		}
	}
	return out
}
