package diagnostics

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// redactedPlaceholder replaces any Field value not explicitly marked safe
// (MEG-015 §09 — Local Logs: "must not include personal data or secrets").
const redactedPlaceholder = "[REDACTED]"

// Field is one structured field of a log entry. Its Value is written
// verbatim only when Redaction is domain.RedactionNone; any other class —
// including the zero value — is replaced with redactedPlaceholder before
// the entry is ever serialized. This fails closed the same way
// domain.RedactionClass already documents for event payloads: a field a
// caller forgot to classify is redacted, not leaked. This is the same rule
// the Secret Broker enforces one layer down (MEG-015 §08 — "Application
// services and Modules must not read secret files directly" /
// "[secret] values must not be observable", MEG-005 §19): a resolved
// secret.Value must never be passed to String — always Secret, if it must
// be logged at all — so it is redacted here even if a caller mistakenly
// tries to log it as plain text.
type Field struct {
	Key       string
	Value     string
	Redaction domain.RedactionClass
}

// String builds a Field that is written verbatim — for values that are
// always safe: component/Module identifiers, states, counts. Never use
// this for anything that could be personal data or a secret.
func String(key, value string) Field {
	return Field{Key: key, Value: value, Redaction: domain.RedactionNone}
}

// Sensitive builds a Field that is redacted from output — for values that
// may carry personal or identifying data.
func Sensitive(key, value string) Field {
	return Field{Key: key, Value: value, Redaction: domain.RedactionSensitive}
}

// Secret builds a Field that is redacted from output — for values that may
// carry credential or secret material. A resolved SecretBroker value must
// only ever be logged through this constructor, if at all.
func Secret(key, value string) Field {
	return Field{Key: key, Value: value, Redaction: domain.RedactionSecret}
}

// redactedValue returns f's value, or redactedPlaceholder unless f is
// explicitly classed safe. An empty value has nothing to redact, so it is
// left as-is regardless of class — this keeps a healthy, reason-less
// health check's log entry legible instead of showing a misleading
// "[REDACTED]" for a field that never carried anything.
func (f Field) redactedValue() string {
	if f.Value == "" || f.Redaction == domain.RedactionNone {
		return f.Value
	}
	return redactedPlaceholder
}

// entry is one JSON-Lines structured log record (MEG-015 §09 — Local
// Logs: "write local .log files with structured fields... include
// component and Module identifiers where available").
type entry struct {
	Time      string            `json:"time"`
	Level     string            `json:"level"`
	Component string            `json:"component"`
	Module    string            `json:"module,omitempty"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// Logger writes structured, redacted log entries to a local .log file, one
// JSON object per line.
type Logger struct {
	mu     sync.Mutex
	out    io.Writer
	closer io.Closer
	clock  func() time.Time
}

// NewFileLogger opens (creating and appending to) a local .log file at
// path, creating its parent directory if needed.
func NewFileLogger(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{out: f, closer: f, clock: func() time.Time { return time.Now().UTC() }}, nil
}

// NewWriterLogger builds a Logger over an arbitrary io.Writer — tests use
// this to capture and inspect output without touching the filesystem.
func NewWriterLogger(w io.Writer) *Logger {
	return &Logger{out: w, clock: func() time.Time { return time.Now().UTC() }}
}

// Close releases the underlying file, if NewFileLogger opened one. It is a
// no-op for a NewWriterLogger-backed Logger.
func (l *Logger) Close() error {
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
}

// Log writes one structured entry. component and module identify the
// origin (MEG-015 §09 — "include component and Module identifiers where
// available"); module may be empty for Platform-originated entries. Every
// field's value is redacted unless explicitly built with String — Log
// itself never decides what is safe, so a caller cannot bypass redaction
// by choosing a different call site.
func (l *Logger) Log(level, component, module, message string, fields ...Field) {
	fieldMap := make(map[string]string, len(fields))
	for _, f := range fields {
		fieldMap[f.Key] = f.redactedValue()
	}
	e := entry{
		Time:      l.clock().Format(time.RFC3339Nano),
		Level:     level,
		Component: component,
		Module:    module,
		Message:   message,
		Fields:    fieldMap,
	}
	line, err := json.Marshal(e)
	if err != nil {
		// Logging must never crash the caller; a malformed entry is
		// dropped rather than panicking or propagating an error a caller
		// would have to handle at every log call site.
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(line)
}

// Info writes an info-level entry for component (no Module).
func (l *Logger) Info(component, message string, fields ...Field) {
	l.Log("info", component, "", message, fields...)
}

// InfoModule writes an info-level entry attributed to a Module running
// under component.
func (l *Logger) InfoModule(component, module, message string, fields ...Field) {
	l.Log("info", component, module, message, fields...)
}

// Error writes an error-level entry for component (no Module).
func (l *Logger) Error(component, message string, fields ...Field) {
	l.Log("error", component, "", message, fields...)
}

// ComponentHealthFields builds the Field set for logging a
// domain.ComponentHealth snapshot: identifiers and state are always safe
// (String); DegradedReason is free text a reporter attached and is logged
// according to health.RedactionClass, exactly as a support bundle would
// treat it (MEG-015 §09).
func ComponentHealthFields(health domain.ComponentHealth) []Field {
	fields := []Field{
		String("lifecycle", string(health.Lifecycle)),
		String("health", string(health.Health)),
		String("last_failure_category", health.LastFailureCategory),
	}
	if health.RedactionClass == domain.RedactionNone {
		fields = append(fields, String("degraded_reason", health.DegradedReason))
	} else {
		fields = append(fields, Field{Key: "degraded_reason", Value: health.DegradedReason, Redaction: health.RedactionClass})
	}
	return fields
}
