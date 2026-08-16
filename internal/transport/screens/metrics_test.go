// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// TestMetricsScreenListsEachSeries pins the client path that makes the metric
// surface built rather than owed. A capability with no client path is on the
// register, and a metric nobody can look at is precisely the discarding counter
// sdk#5 refused to publish.
func TestMetricsScreenListsEachSeries(t *testing.T) {
	fake := &fakeQueries{
		settingsUI:       minimalSettingsUI(),
		canReadTelemetry: true,
		expertModeOn:     true,
		metrics: []telemetry.MetricSeries{
			{Scope: "stremio", Instrument: "addon_requests", Kind: telemetry.MetricKindCounter,
				Dimensions: "addon=cinemeta", Total: 42},
			{Scope: "tmdb", Instrument: "payload", Kind: telemetry.MetricKindHistogram,
				Unit: "By", Count: 3, Total: 90, Min: 10, Max: 60},
		},
	}
	svc := &Service{content: fake}

	rendered := nodeText(render(t, svc, screenMetrics, nil))

	for _, want := range []string{"addon_requests", "addon=cinemeta", "42", "payload"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the screen does not carry %q: %s", want, rendered)
		}
	}
	// The scope is how a reader tells whose instrument this is — two modules
	// may reasonably pick the same instrument name, so the grouping is what
	// disambiguates them.
	for _, scope := range []string{"stremio", "tmdb"} {
		if !strings.Contains(rendered, scope) {
			t.Errorf("the screen does not group by scope %q: %s", scope, rendered)
		}
	}
}

// TestMetricsScreenShowsAHistogramsShapeRatherThanAnAverage pins that a
// histogram must not be reduced to one number. Its value slot carries the
// observation count and the spread goes in the summary, because an average
// presented as the value is the summarisation a histogram exists to avoid.
func TestMetricsScreenShowsAHistogramsShapeRatherThanAnAverage(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: true, expertModeOn: true,
		metrics: []telemetry.MetricSeries{
			{Scope: "tmdb", Instrument: "payload", Kind: telemetry.MetricKindHistogram,
				Unit: "By", Count: 3, Total: 90, Min: 10, Max: 60},
		},
	}
	svc := &Service{content: fake}

	rendered := nodeText(render(t, svc, screenMetrics, nil))

	for _, want := range []string{"min 10", "mean 30", "max 60", "bytes"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the distribution is not shown (%q missing): %s", want, rendered)
		}
	}
}

// TestMetricsScreenSaysTheValuesAreProcessLifetime pins that an empty list has
// two causes that look identical — nothing has been recorded, and the process
// restarted — so the screen says which is possible rather than letting a reader
// conclude the first.
func TestMetricsScreenSaysTheValuesAreProcessLifetime(t *testing.T) {
	fake := &fakeQueries{settingsUI: minimalSettingsUI(), canReadTelemetry: true, expertModeOn: true}
	svc := &Service{content: fake}

	rendered := nodeText(render(t, svc, screenMetrics, nil))

	if !strings.Contains(rendered, "reset on restart") {
		t.Errorf("the screen does not state that the values are process-lifetime: %s", rendered)
	}
}

// TestMetricsNavRowFollowsTheDiagnosticsGate pins that the nav row appears with
// the other diagnostics rows, on the same permission and behind the same
// expert-mode preference — a control nobody may use should not be drawn
// (platform#36).
func TestMetricsNavRowFollowsTheDiagnosticsGate(t *testing.T) {
	withTelemetry := &fakeQueries{settingsUI: minimalSettingsUI(), canReadTelemetry: true, expertModeOn: true}
	if _, ok := findNavItem(render(t, &Service{content: withTelemetry}, screenSettings, nil), "Metrics"); !ok {
		t.Error("no Metrics row for a caller who may read telemetry")
	}

	without := &fakeQueries{settingsUI: minimalSettingsUI(), canReadTelemetry: false, expertModeOn: true}
	if _, ok := findNavItem(render(t, &Service{content: without}, screenSettings, nil), "Metrics"); ok {
		t.Error("a Metrics row was drawn for a caller who may not read telemetry")
	}
}
