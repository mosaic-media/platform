// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is one observation, after redaction and before serialization.
type Record struct {
	Time      time.Time
	Level     Level
	Component string
	Module    string
	Message   string
	Fields    []Field
	Resource  Resource
	Trace     TraceContext
}

// Sink receives records. An implementation must be safe for concurrent use and
// must never panic: a sink that can crash the process turns observation into a
// liability. It returns nothing, because there is no caller in a position to
// handle a logging failure — every call site would have to ignore it anyway.
type Sink interface {
	Write(Record)
}

// entry is the JSON-Lines wire shape: one object per line.
type entry struct {
	Time      string         `json:"time"`
	Level     string         `json:"level"`
	Service   string         `json:"service,omitempty"`
	Instance  string         `json:"instance,omitempty"`
	Boot      string         `json:"boot,omitempty"`
	Trace     string         `json:"trace,omitempty"`
	Span      string         `json:"span,omitempty"`
	Component string         `json:"component,omitempty"`
	Module    string         `json:"module,omitempty"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// JSONSink writes records as JSON Lines. This is the durable local sink of
// ADR 0058: it is crash-survivable, it is the source of truth for a support
// bundle, and it keeps working when the database this process depends on does
// not — which is the case where it matters most.
type JSONSink struct {
	mu     sync.Mutex
	out    io.Writer
	closer io.Closer
}

// NewFileSink opens (creating and appending to) a .log file at path, creating
// its parent directory if needed.
func NewFileSink(path string) (*JSONSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONSink{out: f, closer: f}, nil
}

// NewJSONSink builds a JSONSink over an arbitrary writer — tests capture and
// inspect output through this without touching the filesystem.
func NewJSONSink(w io.Writer) *JSONSink { return &JSONSink{out: w} }

// Close releases the underlying file, if NewFileSink opened one.
func (s *JSONSink) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

// Write serializes one record. A record that cannot be marshalled is dropped
// rather than panicking or propagating an error every call site would discard.
func (s *JSONSink) Write(r Record) {
	e := entry{
		Time:      r.Time.UTC().Format(time.RFC3339Nano),
		Level:     r.Level.String(),
		Service:   r.Resource.ServiceName,
		Instance:  r.Resource.InstanceID,
		Boot:      r.Resource.BootID,
		Trace:     r.Trace.TraceIDString(),
		Span:      r.Trace.SpanIDString(),
		Component: r.Component,
		Module:    r.Module,
		Message:   r.Message,
	}
	if len(r.Fields) > 0 {
		e.Fields = make(map[string]any, len(r.Fields))
		for _, f := range r.Fields {
			e.Fields[f.Key] = f.EmitValue()
		}
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(line)
}

// ConsoleSink writes a compact human-readable line. It exists because the
// composition root's boot narration is read by a person at a terminal, and
// turning that into JSON would cost more legibility than the structure buys —
// so the same records render one way to a person and another to a file. It is
// a formatting choice at one sink, not a second logging path.
type ConsoleSink struct {
	mu  sync.Mutex
	out io.Writer
}

// NewConsoleSink builds a ConsoleSink over w.
func NewConsoleSink(w io.Writer) *ConsoleSink { return &ConsoleSink{out: w} }

// Write renders one record as "level component: message key=value …".
func (s *ConsoleSink) Write(r Record) {
	var b strings.Builder
	b.WriteString(r.Level.String())
	b.WriteByte(' ')
	// A short trace prefix, so two lines from one request are recognisable as
	// such by eye at a terminal. The full id is in the JSON sink; eight
	// characters is enough to spot a pair and short enough not to dominate.
	if id := r.Trace.TraceIDString(); id != "" {
		b.WriteByte('[')
		b.WriteString(id[:8])
		b.WriteString("] ")
	}
	if r.Component != "" {
		b.WriteString(r.Component)
		if r.Module != "" {
			b.WriteByte('/')
			b.WriteString(r.Module)
		}
		b.WriteString(": ")
	}
	b.WriteString(r.Message)

	// Sorted, so two runs of the same code produce comparable lines and a
	// human scanning a terminal finds the same field in the same place.
	fields := append([]Field(nil), r.Fields...)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	for _, f := range fields {
		fmt.Fprintf(&b, " %s=%v", f.Key, f.EmitValue())
	}
	b.WriteByte('\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.out, b.String())
}

// MultiSink fans one record out to several sinks. This is what makes ADR 0058's
// dual-sink rule expressible: a record goes to the durable file *and* to
// whatever else is configured, with neither optional.
type MultiSink []Sink

// Write passes r to every sink in order.
func (m MultiSink) Write(r Record) {
	for _, s := range m {
		if s != nil {
			s.Write(r)
		}
	}
}

// discardSink drops everything. It backs the no-op logger From returns for an
// unseeded context.
type discardSink struct{}

func (discardSink) Write(Record) {}

// IsTerminal reports whether w is an interactive terminal, which is how the
// composition root chooses the console rendering over JSON on stdout.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
