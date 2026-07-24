// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package diagnostics_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

var testNow = time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

// fakeCredential is a deliberately secret-shaped string. The support bundle
// must never carry it verbatim. It is declared here rather than shared with
// the telemetry package's identical constant on purpose: a test that proves
// something is not leaked should not depend on another package to supply the
// thing it is looking for.
const fakeCredential = "hunter2-super-secret-password-AKIAFAKEEXAMPLE1234"

func TestBuildSupportBundleRedactsAnythingNotExplicitlyNone(t *testing.T) {
	components := []domain.ComponentHealth{
		{
			Component:      "postgres",
			Health:         domain.HealthDegraded,
			DegradedReason: "dsn=postgres://admin:" + fakeCredential + "@db/prod unreachable",
			RedactionClass: domain.RedactionSensitive,
		},
		{
			Component:      "event-bus",
			Health:         domain.HealthHealthy,
			DegradedReason: "",
			RedactionClass: domain.RedactionNone,
		},
	}

	bundle := diagnostics.BuildSupportBundle("mosaic-platform", "v1", components, diagnostics.ModuleEgressPosture{Enforced: true, Detail: "test"}, testNow)

	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), fakeCredential) {
		t.Fatalf("support bundle contains the secret-shaped value verbatim: %s", data)
	}

	byComponent := map[string]domain.ComponentHealth{}
	for _, c := range bundle.Components {
		byComponent[c.Component] = c
	}
	if byComponent["postgres"].DegradedReason == components[0].DegradedReason {
		t.Fatal("expected the Sensitive component's DegradedReason to be replaced")
	}
	if byComponent["event-bus"].DegradedReason != "" {
		t.Fatalf("expected the RedactionNone component's DegradedReason to survive unchanged, got %q", byComponent["event-bus"].DegradedReason)
	}
	// Program and Module identification must still be present — a support
	// bundle is anonymised, not unidentifiable.
	if bundle.ProgramID != "mosaic-platform" {
		t.Fatalf("ProgramID = %q, want mosaic-platform", bundle.ProgramID)
	}
	if byComponent["postgres"].Component != "postgres" || byComponent["event-bus"].Component != "event-bus" {
		t.Fatal("expected component identifiers to survive redaction")
	}
	if byComponent["postgres"].Health != domain.HealthDegraded {
		t.Fatal("expected health state to survive redaction")
	}
}

func TestWriteSupportBundlePersistsAnonymisedJSON(t *testing.T) {
	components := []domain.ComponentHealth{
		{Component: "postgres", Health: domain.HealthHealthy, RedactionClass: domain.RedactionNone},
	}
	bundle := diagnostics.BuildSupportBundle("mosaic-platform", "v1", components, diagnostics.ModuleEgressPosture{Enforced: true, Detail: "test"}, testNow)

	path := filepath.Join(t.TempDir(), "bundles", "bundle.json")
	if err := diagnostics.WriteSupportBundle(path, bundle); err != nil {
		t.Fatalf("WriteSupportBundle: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got diagnostics.SupportBundle
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ProgramID != "mosaic-platform" || len(got.Components) != 1 {
		t.Fatalf("got = %+v", got)
	}
}
