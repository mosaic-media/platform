// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// fakeCredential is a deliberately secret-shaped string. Every redaction test
// below asserts on this exact value: the standing exit criterion is that
// something secret-shaped, logged through any path, does NOT appear in output.
const fakeCredential = "hunter2-super-secret-password-AKIAFAKEEXAMPLE1234"

// newTestLogger builds a Logger over a buffer with a fixed resource, so tests
// assert on record content rather than on per-process identity.
func newTestLogger(buf *bytes.Buffer) *telemetry.Logger {
	return telemetry.New(
		telemetry.NewJSONSink(buf),
		telemetry.Resource{ServiceName: "mosaic-platform", InstanceID: "test", BootID: "boot"},
		telemetry.LevelDebug,
	)
}

func TestLoggerNeverWritesSecretOrSensitiveFieldValues(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf).For("postgres")

	logger.Info("connection attempt failed",
		telemetry.String("component", "postgres"),
		telemetry.Secret("dsn_password", fakeCredential),
		telemetry.Sensitive("username", "alice@example.com"),
	)

	output := buf.String()
	if strings.Contains(output, fakeCredential) {
		t.Fatalf("log output contains the secret-shaped value verbatim: %s", output)
	}
	if strings.Contains(output, "alice@example.com") {
		t.Fatalf("log output contains the sensitive value verbatim: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("expected the redaction placeholder in output: %s", output)
	}
	// The safe identifier must still be present — redaction must not swallow
	// everything indiscriminately.
	if !strings.Contains(output, `"postgres"`) {
		t.Fatalf("expected the safe component field to survive redaction: %s", output)
	}
}

// TestSensitiveDropsTheValueAtConstruction is the property ADR 0056 adds over
// the previous redact-on-emit design: the sensitive string is gone before a
// Field exists, so it is never buffered, queued or written anywhere — not just
// masked on the way out.
func TestSensitiveDropsTheValueAtConstruction(t *testing.T) {
	f := telemetry.Sensitive("username", "alice@example.com")
	if v, ok := f.Value.(string); !ok || strings.Contains(v, "alice") {
		t.Fatalf("Sensitive must not carry the original value forward, got %v", f.Value)
	}
	s := telemetry.Secret("password", fakeCredential)
	if v, ok := s.Value.(string); !ok || strings.Contains(v, "hunter2") {
		t.Fatalf("Secret must not carry the original value forward, got %v", s.Value)
	}
}

func TestLoggerRedactsByDefaultForAnUnclassifiedField(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf).For("postgres")

	// A Field built as a struct literal rather than through a constructor has
	// the zero-value RedactionClass (""), which is not RedactionNone and must
	// therefore redact on emit — fail closed, not fail open. Construction-time
	// redaction cannot catch this case, which is exactly why emit still checks.
	unclassified := telemetry.Field{Key: "raw", Value: fakeCredential}
	logger.Info("msg", unclassified)
	if strings.Contains(buf.String(), fakeCredential) {
		t.Fatalf("an unclassified field must redact by default, got: %s", buf.String())
	}
}

func TestLoggerWritesComponentAndModuleIdentifiers(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf).ForModule("composition-root", "anime-module")
	logger.Info("module registered")

	line := parseLogLine(t, buf.Bytes())
	if line["component"] != "composition-root" {
		t.Fatalf("component = %v, want composition-root", line["component"])
	}
	if line["module"] != "anime-module" {
		t.Fatalf("module = %v, want anime-module", line["module"])
	}
}

func TestLoggerStampsProcessIdentity(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).For("boot").Info("ready")

	line := parseLogLine(t, buf.Bytes())
	if line["service"] != "mosaic-platform" {
		t.Fatalf("service = %v, want mosaic-platform", line["service"])
	}
	if line["boot"] != "boot" {
		t.Fatalf("boot = %v, want boot", line["boot"])
	}
}

func TestLoggerDoesNotRedactAnEmptyValue(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).For("postgres").Info("health check", telemetry.Sensitive("degraded_reason", ""))

	line := parseLogLine(t, buf.Bytes())
	fields, _ := line["fields"].(map[string]interface{})
	if fields["degraded_reason"] != "" {
		t.Fatalf("expected an empty value to stay empty rather than show a placeholder, got %v", fields["degraded_reason"])
	}
}

func TestLoggerStringFieldIsWrittenVerbatim(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).For("postgres").Info("schema check", telemetry.String("schema_version", "11"))

	if !strings.Contains(buf.String(), `"schema_version":"11"`) {
		t.Fatalf("expected the safe field verbatim, got: %s", buf.String())
	}
}

// TestLoggerWritesTypedValues covers the generalization from string to any:
// a count should be a JSON number, not a pre-formatted string, so a store can
// index it and a viewer can compare it.
func TestLoggerWritesTypedValues(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).For("session").Info("reaped",
		telemetry.Int("removed", 3), telemetry.Bool("rebuild", true))

	line := parseLogLine(t, buf.Bytes())
	fields, _ := line["fields"].(map[string]interface{})
	if fields["removed"] != float64(3) {
		t.Fatalf("removed = %#v, want the number 3", fields["removed"])
	}
	if fields["rebuild"] != true {
		t.Fatalf("rebuild = %#v, want the boolean true", fields["rebuild"])
	}
}

func TestIdentifierIsStableAndDoesNotLeakTheValue(t *testing.T) {
	a := telemetry.Identifier("session", "session-abc-123")
	b := telemetry.Identifier("session", "session-abc-123")
	c := telemetry.Identifier("session", "session-xyz-789")

	if a.Value != b.Value {
		t.Fatalf("the same subject must digest identically: %v vs %v", a.Value, b.Value)
	}
	if a.Value == c.Value {
		t.Fatalf("different subjects must not collide: %v", a.Value)
	}
	if v, _ := a.Value.(string); strings.Contains(v, "session-abc-123") {
		t.Fatalf("Identifier must not carry the original value: %v", a.Value)
	}
}

func TestIdentifierSurvivesEmit(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).For("session").Info("attached", telemetry.Identifier("session", "abc"))

	line := parseLogLine(t, buf.Bytes())
	fields, _ := line["fields"].(map[string]interface{})
	got, _ := fields["session"].(string)
	// An Identifier is already redacted at construction, so emit must pass the
	// digest through rather than replacing it with the placeholder — otherwise
	// correlation, the entire reason the class exists, is lost.
	if !strings.HasPrefix(got, "id:") {
		t.Fatalf("expected the digest to survive emit, got %q", got)
	}
}

func TestLoggerLevelThresholdDiscardsBelowMinimum(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.New(telemetry.NewJSONSink(&buf), telemetry.Resource{}, telemetry.LevelWarn)
	logger.Info("should not appear")
	if buf.Len() != 0 {
		t.Fatalf("info must be discarded below a warn threshold, got: %s", buf.String())
	}
	logger.Error("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Fatalf("error must survive a warn threshold, got: %s", buf.String())
	}
}

// TestWithDoesNotLeakFieldsBetweenSiblings guards the derive() copy: two
// loggers derived from one parent must not see each other's bound fields, or a
// request's scope bleeds into an unrelated request's records.
func TestLoggerWithDoesNotLeakFieldsBetweenSiblings(t *testing.T) {
	var buf bytes.Buffer
	parent := newTestLogger(&buf).For("session")

	a := parent.With(telemetry.String("lane", "a"))
	b := parent.With(telemetry.String("lane", "b"))

	buf.Reset()
	a.Info("one")
	if strings.Contains(buf.String(), `"lane":"b"`) {
		t.Fatalf("a sibling's bound field leaked into this record: %s", buf.String())
	}
	buf.Reset()
	b.Info("two")
	if strings.Contains(buf.String(), `"lane":"a"`) {
		t.Fatalf("a sibling's bound field leaked into this record: %s", buf.String())
	}
}

func TestComponentHealthFieldsRedactsDegradedReasonUnlessNone(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf).For("postgres")

	health := domain.ComponentHealth{
		Component:      "postgres",
		Health:         domain.HealthDegraded,
		DegradedReason: "dsn=postgres://admin:" + fakeCredential + "@db/prod contains secret",
		RedactionClass: domain.RedactionSensitive,
	}
	logger.Info("health check", telemetry.ComponentHealthFields(health)...)

	if strings.Contains(buf.String(), fakeCredential) {
		t.Fatalf("a Sensitive-classed DegradedReason must not appear verbatim in logs, got: %s", buf.String())
	}

	buf.Reset()
	safeHealth := domain.ComponentHealth{
		Component:      "postgres",
		Health:         domain.HealthDegraded,
		DegradedReason: "schema behind by 2 migrations",
		RedactionClass: domain.RedactionNone,
	}
	logger.Info("health check", telemetry.ComponentHealthFields(safeHealth)...)
	if !strings.Contains(buf.String(), "schema behind by 2 migrations") {
		t.Fatalf("a RedactionNone DegradedReason should be written verbatim, got: %s", buf.String())
	}
}

func parseLogLine(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	line := bytes.TrimSpace(data)
	// Only the last line matters when a test writes more than one entry.
	if idx := bytes.LastIndexByte(line, '\n'); idx >= 0 {
		line = line[idx+1:]
	}
	var m map[string]interface{}
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("log output is not valid JSON: %v (line: %s)", err, line)
	}
	return m
}
