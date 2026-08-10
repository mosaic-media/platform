// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The instrument viewer (sdk#9).
//
// **This screen is the reason the metric surface counts as built.** A capability
// with no client path is owed rather than done, and a metric nobody can look at
// is the specific thing sdk#5 refused to publish — a module author
// instruments against it, gets no data, and has nothing telling them why. So the
// surface and the screen land together.
//
// It is composed entirely from vocabulary that already exists: `SettingsFrame`,
// `Section`, `StatCard`, `SettingsRow`, `EmptyState`. No component was added to
// a client and no definition was added to the contract, which is what a new
// screen is supposed to cost.

// metricsScreen lists every instrument's current value.
func (s *Service) metricsScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	nav, navErr := s.settingsNav(ctx, caller)
	if navErr != nil {
		return nil, navErr
	}
	nav.selected = true

	res, err := s.content.ListMetrics(ctx, app.ListMetricsQuery{Caller: caller})
	if err != nil {
		return nil, err
	}

	// Said on the screen rather than only in a doc comment, because it is the
	// difference between "this module has done nothing" and "this process was
	// restarted", and a reader who does not know that will draw the wrong
	// conclusion from an empty list.
	const lead = "Counters and histograms recorded since this process started. They are held in " +
		"memory and reset on restart."

	if len(res.Series) == 0 {
		return settingsFrame(nav, sectionMetrics, "Metrics", lead,
			ui.EmptyState(emptyIconSearch, "No instruments recorded yet").Build()), nil
	}

	body := make([]sdui.Node, 0, 4)
	body = append(body, metricTotals(res.Series).Build())

	// Grouped by scope, because "which module is this" is the first question a
	// reader has and the scope is the only thing that answers it — an
	// instrument name is chosen by whoever recorded it and two modules may
	// reasonably pick the same one.
	for _, scope := range scopesOf(res.Series) {
		rows := make([]ui.El, 0, 8)
		for _, series := range res.Series {
			if series.Scope != scope {
				continue
			}
			rows = append(rows, ui.SettingsRow(series.Instrument,
				ui.Summary(metricSummary(series)),
				ui.Value(metricValue(series))))
		}
		body = append(body, ui.Section(scope, rows...).Build())
	}
	return settingsFrame(nav, sectionMetrics, "Metrics", lead, body...), nil
}

// metricTotals is the row of figures above the list: how much is being recorded,
// and by how many scopes.
//
// The series count is the one worth showing, because it is the number that has a
// cap on it (sdk#9) and the only way a reader finds out they are approaching
// one before the fold happens.
func metricTotals(series []telemetry.MetricSeries) *ui.Element {
	counters, histograms := 0, 0
	for _, s := range series {
		if s.Kind == telemetry.MetricKindHistogram {
			histograms++
			continue
		}
		counters++
	}
	return ui.Stack("horizontal", 4,
		ui.StatCard("Series", strconv.Itoa(len(series)),
			ui.Summary(fmt.Sprintf("cap %d per scope", telemetry.MetricSeriesPerScope))),
		ui.StatCard("Counters", strconv.Itoa(counters)),
		ui.StatCard("Histograms", strconv.Itoa(histograms)),
		ui.StatCard("Scopes", strconv.Itoa(len(scopesOf(series)))),
	)
}

// scopesOf lists the distinct scopes in order. The series are already sorted by
// scope, so this preserves that order rather than imposing a second one.
func scopesOf(series []telemetry.MetricSeries) []string {
	var out []string
	var last string
	for i, s := range series {
		if i == 0 || s.Scope != last {
			out = append(out, s.Scope)
			last = s.Scope
		}
	}
	return out
}

// metricValue renders the number a reader is looking for.
//
// A counter has one — its total. A histogram has no single value, so the count
// is what goes in the value slot and the distribution goes in the summary: an
// average presented as *the* value is exactly the summarisation a histogram
// exists to avoid.
func metricValue(series telemetry.MetricSeries) string {
	if series.Kind == telemetry.MetricKindHistogram {
		return strconv.FormatUint(series.Count, 10)
	}
	return formatMetricNumber(series.Total)
}

// metricSummary is the line beneath the instrument name: its dimensions, and for
// a histogram the shape of what was observed.
func metricSummary(series telemetry.MetricSeries) string {
	parts := make([]string, 0, 3)
	if series.Dimensions != "" {
		parts = append(parts, series.Dimensions)
	} else {
		parts = append(parts, "no dimensions")
	}
	if series.Kind == telemetry.MetricKindHistogram && series.Count > 0 {
		mean := series.Total / float64(series.Count)
		parts = append(parts, fmt.Sprintf("min %s · mean %s · max %s",
			formatMetricNumber(series.Min),
			formatMetricNumber(mean),
			formatMetricNumber(series.Max)))
	}
	if series.Unit != "" {
		parts = append(parts, unitLabel(series.Unit))
	}
	return strings.Join(parts, " · ")
}

// unitLabel renders a unit annotation for a person.
//
// The wire values are OpenTelemetry's — `By`, `{item}` — which are correct for a
// backend and unreadable on a screen. Anything unrecognised is shown as given
// rather than dropped: a unit this build does not know is still information, and
// inventing a translation for it would be worse.
func unitLabel(unit string) string {
	switch unit {
	case "s":
		return "seconds"
	case "By":
		return "bytes"
	case "{item}":
		return "items"
	default:
		return unit
	}
}

// formatMetricNumber writes a value without trailing noise: whole numbers as
// integers, everything else to two decimals. A counter is almost always whole,
// and `1.00` where a reader expects `1` reads as a different kind of quantity.
func formatMetricNumber(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
