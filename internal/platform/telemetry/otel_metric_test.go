// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// A counter accumulates and comes back with its total, which is the whole claim
// the surface makes.
func TestACounterAccumulates(t *testing.T) {
	c := telemetry.NewMetricCollector()

	for range 3 {
		if _, err := c.Count("stremio", "addon_requests", 2, []telemetry.Field{
			telemetry.String("addon", "cinemeta"),
		}); err != nil {
			t.Fatalf("Count: %v", err)
		}
	}

	series := snapshot(t, c)
	if len(series) != 1 {
		t.Fatalf("want one series, got %d: %+v", len(series), series)
	}
	if series[0].Total != 6 {
		t.Errorf("total is %v, want 6", series[0].Total)
	}
	if series[0].Kind != telemetry.MetricKindCounter {
		t.Errorf("kind is %q", series[0].Kind)
	}
	// The scope is the attribution, and it is stamped by the caller of Count
	// rather than taken from anything a module said.
	if series[0].Scope != "stremio" {
		t.Errorf("scope is %q, want the module id", series[0].Scope)
	}
	if series[0].Dimensions != "addon=cinemeta" {
		t.Errorf("dimensions rendered as %q", series[0].Dimensions)
	}
}

// A histogram keeps the shape a counter cannot: how many, and how they were
// spread. Recording the mean instead is the summarisation a histogram exists to
// avoid, so min, max, sum and count all have to survive.
func TestAHistogramKeepsItsDistribution(t *testing.T) {
	c := telemetry.NewMetricCollector()

	for _, v := range []float64{10, 20, 60} {
		if _, err := c.Measure("tmdb", "payload", v, "By", nil); err != nil {
			t.Fatalf("Measure: %v", err)
		}
	}

	series := snapshot(t, c)
	if len(series) != 1 {
		t.Fatalf("want one series, got %d", len(series))
	}
	got := series[0]
	if got.Kind != telemetry.MetricKindHistogram {
		t.Fatalf("kind is %q", got.Kind)
	}
	if got.Count != 3 || got.Total != 90 || got.Min != 10 || got.Max != 60 {
		t.Errorf("distribution is count=%d sum=%v min=%v max=%v, want 3/90/10/60",
			got.Count, got.Total, got.Min, got.Max)
	}
	if got.Unit != "By" {
		t.Errorf("unit is %q", got.Unit)
	}
}

// The cap is what stops third-party code exhausting the host, and the thing it
// must not do is lose the measurement: a counter that under-reports is worse
// than one whose breakdown is coarse, because the number a person reads is
// then quietly wrong.
func TestTheSeriesCapFoldsRatherThanDrops(t *testing.T) {
	c := telemetry.NewMetricCollector()

	// One more distinct dimension value than the cap allows, each contributing
	// 1 to the same instrument.
	const beyond = telemetry.MetricSeriesPerScope + 50
	refusals := 0
	for i := range beyond {
		folded, err := c.Count("chatty", "per_title", 1, []telemetry.Field{
			telemetry.String("title", "title-"+strings.Repeat("x", i%7)+itoa(i)),
		})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if folded {
			refusals++
		}
	}

	series := snapshot(t, c)
	if len(series) > telemetry.MetricSeriesPerScope+1 {
		t.Errorf("the scope created %d series against a cap of %d",
			len(series), telemetry.MetricSeriesPerScope)
	}

	// Nothing was lost: every observation is somewhere in the totals.
	var total float64
	for _, s := range series {
		total += s.Total
	}
	if total != beyond {
		t.Errorf("the totals sum to %v, want %d — observations were dropped rather than folded",
			total, beyond)
	}

	// And the fold is announced exactly once, so a module in a loop cannot turn
	// the diagnostic into the flood the cap prevents.
	if refusals != 1 {
		t.Errorf("the cap was reported %d times, want exactly 1", refusals)
	}
}

// Repeatedly writing a series that already exists must not count against the
// cap: the bound is on how many series exist, not on how often they are written.
func TestRewritingAnExistingSeriesDoesNotConsumeTheCap(t *testing.T) {
	c := telemetry.NewMetricCollector()

	for range telemetry.MetricSeriesPerScope * 4 {
		folded, err := c.Count("steady", "requests", 1, []telemetry.Field{
			telemetry.String("addon", "cinemeta"),
		})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if folded {
			t.Fatal("a repeat write of one series hit the cap")
		}
	}
	if series := snapshot(t, c); len(series) != 1 {
		t.Errorf("want one series, got %d", len(series))
	}
}

// Redaction applies to a dimension exactly as it does to a log field, and it
// matters more here: a record ages out under retention, while a leaked value
// used as a dimension is a permanent label on a running counter.
func TestRedactionAppliesToDimensions(t *testing.T) {
	c := telemetry.NewMetricCollector()

	if _, err := c.Count("stremio", "searches", 1, []telemetry.Field{
		telemetry.String("addon", "cinemeta"),
		telemetry.Sensitive("query", "the terminator"),
		telemetry.Secret("api_key", "sk-live-4242"),
		// A Field built as a struct literal: its class is the zero value rather
		// than RedactionNone, so it must fail closed here too.
		{Key: "hand_written", Value: "postgres://user:hunter2@db/x"},
	}); err != nil {
		t.Fatalf("Count: %v", err)
	}

	dims := snapshot(t, c)[0].Dimensions
	for _, leaked := range []string{"the terminator", "sk-live-4242", "hunter2"} {
		if strings.Contains(dims, leaked) {
			t.Errorf("%q reached a metric dimension: %s", leaked, dims)
		}
	}
	if !strings.Contains(dims, "addon=cinemeta") {
		t.Errorf("the unclassified dimension did not survive: %s", dims)
	}
}

// An instrument OpenTelemetry refuses returns an error rather than discarding in
// silence — the failure sdk#5 declined to publish a metric surface over.
func TestARefusedInstrumentReturnsAnError(t *testing.T) {
	c := telemetry.NewMetricCollector()

	// An empty name is invalid in OpenTelemetry's own validation.
	if _, err := c.Count("stremio", "", 1, nil); err == nil {
		t.Error("an unnamed instrument was accepted")
	}
}

// A nil collector is what an unconfigured process holds, and every method has to
// tolerate it: the module adapter calls straight through without a check, the
// same way the logger's no-op works.
func TestANilCollectorDiscards(t *testing.T) {
	var c *telemetry.MetricCollector
	if _, err := c.Count("m", "x", 1, nil); err != nil {
		t.Errorf("Count on a nil collector: %v", err)
	}
	if _, err := c.Measure("m", "x", 1, "s", nil); err != nil {
		t.Errorf("Measure on a nil collector: %v", err)
	}
	if series, err := c.Snapshot(context.Background()); err != nil || series != nil {
		t.Errorf("Snapshot on a nil collector: %v, %v", series, err)
	}
	if c.Meter("m") != nil {
		t.Error("a nil collector handed out a meter")
	}
}

// The collector travels in the context, the same ambient rule the logger and the
// span sink follow (platform#31), so no layer takes it as a parameter.
func TestTheCollectorIsAmbient(t *testing.T) {
	c := telemetry.NewMetricCollector()
	ctx := telemetry.WithMetrics(context.Background(), c)

	if telemetry.MetricsFrom(ctx) != c {
		t.Error("the collector did not come back out of the context")
	}
	if telemetry.MetricsFrom(context.Background()) != nil {
		t.Error("an unconfigured context produced a collector")
	}
}

func snapshot(t *testing.T, c *telemetry.MetricCollector) []telemetry.MetricSeries {
	t.Helper()
	series, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return series
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// A compile-time reminder that Field's redaction vocabulary is the domain's,
// so this file's struct literal above is the same zero value the sinks see.
var _ domain.RedactionClass = telemetry.Field{}.Redaction
