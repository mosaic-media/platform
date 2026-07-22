// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"time"
)

// Level is a record's severity.
type Level int

const (
	// LevelDebug is detail useful while diagnosing, off by default.
	LevelDebug Level = iota
	// LevelInfo is the normal narration of what the process is doing.
	LevelInfo
	// LevelWarn is a condition that did not stop the operation but should not
	// be normal.
	LevelWarn
	// LevelError is an operation that failed.
	LevelError
)

// String renders the level for a sink.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// ParseLevel resolves a configured level name, falling back to info for an
// unrecognised value. A misspelled level must not silence the process.
func ParseLevel(s string) Level {
	switch s {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Logger emits records to a sink with a set of bound fields. It is immutable:
// With and For return derived loggers rather than mutating the receiver, so a
// logger seeded into a context cannot be altered by something downstream that
// holds it.
type Logger struct {
	sink      Sink
	resource  Resource
	component string
	module    string
	bound     []Field
	min       Level
	clock     func() time.Time
}

// New builds a Logger writing to sink, stamping resource on every record and
// discarding anything below min.
func New(sink Sink, resource Resource, min Level) *Logger {
	if sink == nil {
		sink = discardSink{}
	}
	return &Logger{sink: sink, resource: resource, min: min, clock: time.Now}
}

// nop is the Logger From returns for a context that was never seeded. It is a
// working Logger that discards, not nil — the whole point is that a call site
// never has to check.
var nop = &Logger{sink: discardSink{}, min: LevelError + 1, clock: time.Now}

// For returns a Logger attributed to a component — the Platform-side origin of
// a record ("session", "outbox-worker", "postgres").
func (l *Logger) For(component string) *Logger {
	d := l.derive()
	d.component = component
	return d
}

// ForModule returns a Logger attributed to a module running under component.
// Module attribution is stamped by the Platform at the invocation seam, never
// by the module itself (ADR 0059), which is why this is a Platform-side call.
func (l *Logger) ForModule(component, module string) *Logger {
	d := l.derive()
	d.component = component
	d.module = module
	return d
}

// With returns a Logger carrying fields in addition to those already bound.
// This is how an edge binds request scope once — session, actor, trace — so
// every downstream line inherits it without naming it.
func (l *Logger) With(fields ...Field) *Logger {
	if len(fields) == 0 {
		return l
	}
	d := l.derive()
	d.bound = append(d.bound, fields...)
	return d
}

// derive copies the receiver so a returned Logger never aliases the parent's
// bound slice — two independent With calls on one logger must not see each
// other's fields.
func (l *Logger) derive() *Logger {
	d := *l
	d.bound = append(make([]Field, 0, len(l.bound)+2), l.bound...)
	return &d
}

// Debug emits at LevelDebug.
func (l *Logger) Debug(message string, fields ...Field) { l.emit(LevelDebug, message, fields) }

// Info emits at LevelInfo.
func (l *Logger) Info(message string, fields ...Field) { l.emit(LevelInfo, message, fields) }

// Warn emits at LevelWarn.
func (l *Logger) Warn(message string, fields ...Field) { l.emit(LevelWarn, message, fields) }

// Error emits at LevelError.
func (l *Logger) Error(message string, fields ...Field) { l.emit(LevelError, message, fields) }

// emit assembles and writes one record. message is expected to be a constant:
// a value interpolated into it has bypassed field classification entirely,
// which is how a fail-closed scheme is actually defeated in practice (ADR 0056).
func (l *Logger) emit(level Level, message string, fields []Field) {
	if l == nil || level < l.min {
		return
	}
	all := fields
	if len(l.bound) > 0 {
		all = make([]Field, 0, len(l.bound)+len(fields))
		all = append(all, l.bound...)
		all = append(all, fields...)
	}
	l.sink.Write(Record{
		Time:      l.clock(),
		Level:     level,
		Component: l.component,
		Module:    l.module,
		Message:   message,
		Fields:    all,
		Resource:  l.resource,
	})
}
