// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// These cover the Platform half of ADR 0059: what a module emits becomes a
// Platform record, with attribution the module cannot forge and redaction it
// cannot opt out of.

func moduleTelemetryFixture(buf *bytes.Buffer, moduleID string) v1.Telemetry {
	lg := telemetry.New(telemetry.NewJSONSink(buf), telemetry.Resource{ServiceName: "mosaic-platform"}, telemetry.LevelDebug).
		ForModule("module", moduleID)
	return newModuleTelemetry(lg, moduleID)
}

func lastRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := bytes.TrimSpace(buf.Bytes())
	if i := bytes.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, line)
	}
	return m
}

func TestModuleRecordsAreAttributedToTheModule(t *testing.T) {
	var buf bytes.Buffer
	moduleTelemetryFixture(&buf, "stremio").Info("sourced a title", v1.Int("results", 3))

	rec := lastRecord(t, &buf)
	if rec["module"] != "stremio" {
		t.Fatalf("module = %v, want stremio", rec["module"])
	}
	if rec["component"] != "module" {
		t.Fatalf("component = %v, want module", rec["component"])
	}
	fields, _ := rec["fields"].(map[string]any)
	if fields["results"] != float64(3) {
		t.Fatalf("typed field lost: %#v", fields["results"])
	}
}

// TestModuleCannotForgeAttribution is the security property. A module supplies
// content, never identity: fields named "module" or "component" are ordinary
// data and must not displace the attribution the Platform stamped.
func TestModuleCannotForgeAttribution(t *testing.T) {
	var buf bytes.Buffer
	moduleTelemetryFixture(&buf, "stremio").Info("innocent",
		v1.String("module", "postgres"),
		v1.String("component", "composition-root"))

	rec := lastRecord(t, &buf)
	if rec["module"] != "stremio" || rec["component"] != "module" {
		t.Fatalf("a module overrode its own attribution: module=%v component=%v",
			rec["module"], rec["component"])
	}
}

// TestModuleFieldsObeyPlatformRedaction — the SDK's classes are the Platform's
// classes, and a module cannot opt out of them.
func TestModuleFieldsObeyPlatformRedaction(t *testing.T) {
	const secret = "hunter2-super-secret-password-AKIAFAKEEXAMPLE1234"
	var buf bytes.Buffer
	moduleTelemetryFixture(&buf, "stremio").Warn("addon returned an odd shape",
		v1.Secret("api_key", secret),
		v1.Sensitive("title", "Some Private Documentary"),
		// A struct literal built by hand, bypassing the constructors: its
		// zero-value class is not RedactionNone, so it must fail closed on the
		// way in as well as on the way out.
		v1.Field{Key: "raw", Value: secret})

	out := buf.String()
	for _, forbidden := range []string{secret, "Some Private Documentary"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("a classified module value was recorded verbatim: %s", out)
		}
	}
}

// TestModuleIdentifierIsDigestedByThePlatform covers the one asymmetry: the
// SDK hands the value across because only the Platform holds the salt.
func TestModuleIdentifierIsDigestedByThePlatform(t *testing.T) {
	var buf bytes.Buffer
	moduleTelemetryFixture(&buf, "stremio").Info("addon call", v1.Identifier("addon", "https://example.test/manifest.json"))

	rec := lastRecord(t, &buf)
	fields, _ := rec["fields"].(map[string]any)
	got, _ := fields["addon"].(string)
	if !strings.HasPrefix(got, "id:") {
		t.Fatalf("expected a digest, got %q", got)
	}
	if strings.Contains(got, "example.test") {
		t.Fatalf("the raw value survived digesting: %q", got)
	}
}

// TestModuleQuotaBoundsOneInvocation — third-party code must not be able to
// fill the telemetry store or drown another module's records.
func TestModuleQuotaBoundsOneInvocation(t *testing.T) {
	var buf bytes.Buffer
	tel := moduleTelemetryFixture(&buf, "chatty")

	for i := 0; i < moduleRecordQuota*2; i++ {
		tel.Info("spam")
	}

	lines := bytes.Count(bytes.TrimSpace(buf.Bytes()), []byte("\n")) + 1
	if lines > moduleRecordQuota {
		t.Fatalf("emitted %d records past a quota of %d", lines, moduleRecordQuota)
	}
	// And the exhaustion is itself recorded once, or a module losing its
	// records looks identical to a module that had nothing to say.
	if n := strings.Count(buf.String(), "quota exhausted"); n != 1 {
		t.Fatalf("quota exhaustion recorded %d times, want exactly 1", n)
	}
}

// TestModuleSpanIsPrefixedSoItCannotImpersonatePlatformWork keeps a module
// from naming a span in a way that reads as the Platform's own in a waterfall.
func TestModuleSpanIsPrefixedSoItCannotImpersonatePlatformWork(t *testing.T) {
	captured := &captureModuleSpans{}
	ctx := telemetry.WithSpanSink(context.Background(), captured)
	ctx = telemetry.TraceInto(ctx, telemetry.NewTraceContext())

	var buf bytes.Buffer
	tel := moduleTelemetryFixture(&buf, "stremio")
	_, span := tel.Span(ctx, "tx")
	span.End()

	if len(captured.names) != 1 {
		t.Fatalf("recorded %v", captured.names)
	}
	if captured.names[0] != "module.stremio.tx" {
		t.Fatalf("span name = %q, want it namespaced to the module", captured.names[0])
	}
}

type captureModuleSpans struct{ names []string }

func (c *captureModuleSpans) WriteSpan(r telemetry.SpanRecord) {
	c.names = append(c.names, r.Name)
}

// TestModuleSpanInheritsTheTrace — a module's span must land in the trace of
// the request that invoked it, which is the whole cross-repository point.
func TestModuleSpanInheritsTheTrace(t *testing.T) {
	captured := &captureModuleSpanRecords{}
	tc := telemetry.NewTraceContext()
	ctx := telemetry.TraceInto(telemetry.WithSpanSink(context.Background(), captured), tc)

	var buf bytes.Buffer
	_, span := moduleTelemetryFixture(&buf, "stremio").Span(ctx, "fetch")
	span.End()

	if len(captured.records) != 1 {
		t.Fatalf("recorded %d spans", len(captured.records))
	}
	if got := captured.records[0].Trace.TraceIDString(); got != tc.TraceIDString() {
		t.Fatalf("module span left the trace: %s != %s", got, tc.TraceIDString())
	}
}

type captureModuleSpanRecords struct{ records []telemetry.SpanRecord }

func (c *captureModuleSpanRecords) WriteSpan(r telemetry.SpanRecord) {
	c.records = append(c.records, r)
}
