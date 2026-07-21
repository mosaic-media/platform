// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package diagnostics_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// fakeCredential is a deliberately secret-shaped string — the exit
// criterion this test exists to prove ("write a test that deliberately
// includes something secret-shaped ... and confirms it does NOT appear in
// the output").
const fakeCredential = "hunter2-super-secret-password-AKIAFAKEEXAMPLE1234"

func TestLoggerNeverWritesSecretOrSensitiveFieldValues(t *testing.T) {
	var buf bytes.Buffer
	logger := diagnostics.NewWriterLogger(&buf)

	logger.Info("postgres", "connection attempt failed",
		diagnostics.String("component", "postgres"),
		diagnostics.Secret("dsn_password", fakeCredential),
		diagnostics.Sensitive("username", "alice@example.com"),
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
	// The safe identifier must still be present — redaction must not
	// swallow everything indiscriminately.
	if !strings.Contains(output, `"postgres"`) {
		t.Fatalf("expected the safe component field to survive redaction: %s", output)
	}
}

func TestLoggerRedactsByDefaultForAnUnclassifiedField(t *testing.T) {
	var buf bytes.Buffer
	logger := diagnostics.NewWriterLogger(&buf)

	// A Field built directly (not via String/Sensitive/Secret) has the
	// zero-value RedactionClass (""), which must redact — fail closed, not
	// fail open — even though a caller reached around the constructor
	// helpers.
	unclassified := diagnostics.Field{Key: "raw", Value: fakeCredential}
	logger.Log("info", "postgres", "", "msg", unclassified)
	if strings.Contains(buf.String(), fakeCredential) {
		t.Fatalf("an unclassified field must redact by default, got: %s", buf.String())
	}
}

func TestLoggerWritesComponentAndModuleIdentifiers(t *testing.T) {
	var buf bytes.Buffer
	logger := diagnostics.NewWriterLogger(&buf)
	logger.InfoModule("composition-root", "anime-module", "module registered")

	line := parseLogLine(t, buf.Bytes())
	if line["component"] != "composition-root" {
		t.Fatalf("component = %v, want composition-root", line["component"])
	}
	if line["module"] != "anime-module" {
		t.Fatalf("module = %v, want anime-module", line["module"])
	}
}

func TestLoggerDoesNotRedactAnEmptyValue(t *testing.T) {
	var buf bytes.Buffer
	logger := diagnostics.NewWriterLogger(&buf)
	logger.Info("postgres", "health check", diagnostics.Sensitive("degraded_reason", ""))

	line := parseLogLine(t, buf.Bytes())
	fields, _ := line["fields"].(map[string]interface{})
	if fields["degraded_reason"] != "" {
		t.Fatalf("expected an empty value to stay empty rather than show a placeholder, got %v", fields["degraded_reason"])
	}
}

func TestLoggerStringFieldIsWrittenVerbatim(t *testing.T) {
	var buf bytes.Buffer
	logger := diagnostics.NewWriterLogger(&buf)
	logger.Info("postgres", "schema check", diagnostics.String("schema_version", "11"))

	if !strings.Contains(buf.String(), `"schema_version":"11"`) {
		t.Fatalf("expected the safe field verbatim, got: %s", buf.String())
	}
}

func TestComponentHealthFieldsRedactsDegradedReasonUnlessNone(t *testing.T) {
	var buf bytes.Buffer
	logger := diagnostics.NewWriterLogger(&buf)

	health := domain.ComponentHealth{
		Component:      "postgres",
		Health:         domain.HealthDegraded,
		DegradedReason: "dsn=postgres://admin:" + fakeCredential + "@db/prod contains secret",
		RedactionClass: domain.RedactionSensitive,
	}
	logger.Info("postgres", "health check", diagnostics.ComponentHealthFields(health)...)

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
	logger.Info("postgres", "health check", diagnostics.ComponentHealthFields(safeHealth)...)
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
