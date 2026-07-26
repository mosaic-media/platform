// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The expert-mode surface (ADR 0058): stored telemetry rendered as ordinary
// SDUI, so a second client gets the diagnostics screens for free the same way
// it gets the content ones (ADR 0029).
//
// Everything here reads data that was redacted at construction (ADR 0056), so
// no screen masks anything of its own. What it must still do is treat the text
// as *untrusted*: a message or a field can originate in a third-party module
// (ADR 0059) or in an upstream provider's error, so it is rendered as text and
// never as anything a renderer would interpret.
//
// Reaching any of this requires `telemetry.read`, enforced by the application
// services these call. The affordance that leads here is separately hidden
// from anyone without that grant — a normal user never sees the toggle, let
// alone the screens.

// logsScreen is the log viewer: filter, and a page of records newest first.
func (s *Service) logsScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	nav, navErr := s.settingsNav(ctx, caller)
	if navErr != nil {
		return nil, navErr
	}
	nav.selected = true

	filter := domain.TelemetryLogFilter{
		MinLevel:  stringParam(params, paramLevel),
		Component: stringParam(params, paramComponent),
		Trace:     stringParam(params, paramTrace),
		Contains:  stringParam(params, paramText),
	}
	res, err := s.content.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{Caller: caller, Filter: filter})
	if err != nil {
		return nil, err
	}

	const lead = "Structured records, correlated with the trace that produced them."
	body := make([]sdui.Node, 0, 4)
	if counts := levelCountRow(res.Records); counts != nil {
		body = append(body, counts.Build())
	}
	body = append(body, levelFilterRow(params).Build())
	if len(res.Records) == 0 {
		body = append(body, ui.EmptyState(emptyIconSearch, "No records match that filter in the last day").Build())
		return settingsFrame(nav, sectionLogs, "Logs", lead, body...), nil
	}

	// A table rather than a stack of cards, as the design draws it. The columns
	// are how a log is actually read: the eye runs down one of them looking for
	// the line that is different, which a stack of differently-shaped cards
	// makes impossible.
	rows := make([]any, 0, len(res.Records))
	for _, r := range res.Records {
		row := map[string]any{
			"time":    r.Time.Format("15:04:05"),
			"level":   r.Level,
			"tone":    logColor(r.Level),
			"service": logService(r),
			"message": r.Message,
		}
		if r.Trace != "" {
			row["trace"] = shortID(r.Trace)
			// The trace id is a navigation into the waterfall, which is the move
			// that makes a log line useful: "what else happened because of this?"
			// becomes a tap rather than a copied string.
			row["action"] = ui.Navigate(screenTrace, map[string]any{paramTrace: r.Trace})
		}
		rows = append(rows, row)
	}
	body = append(body, ui.LogTable(ui.Rows(rows)).Build())

	// The footer states what is on screen and how long it will be kept. It does
	// not state a total: the query returns the matching window, and there is no
	// count of everything stored to compare it against.
	footer := strconv.Itoa(len(res.Records)) + " " + plural(len(res.Records), "record")
	body = append(body, ui.Stack("horizontal", 4,
		ui.Component("Text", ui.Prop("text", footer),
			ui.Prop("style", map[string]any{"variant": "xs", "color": "text-faint"})),
	).Build())

	return settingsFrame(nav, sectionLogs, "Logs", lead, body...), nil
}

// levelCountRow is the design's run of level chips with their counts. It counts
// what came back rather than what exists: the query is a filtered window, and a
// chip claiming a total the screen cannot see would be a number nobody could
// check.
func levelCountRow(records []domain.TelemetryLogRecord) *ui.Element {
	counts := map[string]int{}
	for _, r := range records {
		counts[strings.ToLower(r.Level)]++
	}
	chips := make([]ui.El, 0, 4)
	for _, lv := range []string{"info", "warn", "error", "debug"} {
		if counts[lv] == 0 {
			continue
		}
		chips = append(chips, ui.StatCard(strings.ToUpper(lv), strconv.Itoa(counts[lv])))
	}
	if len(chips) == 0 {
		return nil
	}
	return ui.Grid(ui.MinColumnWidth(120), ui.Gap(3), ui.Group(chips...))
}

// logColor is the colour token a level takes in the table.
func logColor(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "danger"
	case "warn", "warning":
		return "warning"
	case "debug":
		return "text-faint"
	default:
		return "text-muted"
	}
}

// logService is what the record says it came from, preferring the module over
// the component: on a line emitted inside an extension, the module is the
// answer to "who said this" and the component is an implementation detail.
func logService(r domain.TelemetryLogRecord) string {
	if r.Module != "" {
		return r.Module
	}
	if r.Component != "" {
		return r.Component
	}
	return r.Service
}

// logRow renders one record. The trace id is a navigation into the waterfall,
// which is the move that makes a log line useful: "what else happened because
// of this?" becomes a tap rather than a copied string.
func logRow(r domain.TelemetryLogRecord) ui.El {
	meta := []string{r.Time.Format("15:04:05.000"), r.Level}
	if r.Component != "" {
		meta = append(meta, r.Component)
	}
	if r.Module != "" {
		meta = append(meta, "module:"+r.Module)
	}

	els := []ui.El{
		ui.Badge(strings.Join(meta, " · "), logTone(r.Level)),
		// The message and its fields as plain text. Both can originate outside
		// the Platform, so neither is ever handed to something that interprets
		// markup.
		ui.Subtitle(r.Message),
	}
	if fields := renderFields(r.Fields); fields != "" {
		els = append(els, ui.Meta(fields))
	}
	if r.Trace != "" {
		els = append(els, ui.Button("Trace "+shortID(r.Trace), "quiet",
			ui.OnTap(ui.Navigate(screenTrace, map[string]any{paramTrace: r.Trace}))))
	}
	return ui.Stack("vertical", 2, els...)
}

// tracesScreen lists recent traces: what ran, how long it took, and whether
// anything inside it failed.
func (s *Service) tracesScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	nav, navErr := s.settingsNav(ctx, caller)
	if navErr != nil {
		return nil, navErr
	}
	nav.selected = true

	filter := domain.TelemetryTraceFilter{Order: domain.TraceOrderRecent}
	if stringParam(params, paramOrder) == string(domain.TraceOrderSlowest) {
		filter.Order = domain.TraceOrderSlowest
	}
	filter.FailedOnly = stringParam(params, paramFailed) == "true"

	res, err := s.content.ListTraces(ctx, app.ListTracesQuery{Caller: caller, Filter: filter})
	if err != nil {
		return nil, err
	}

	const lead = "Spans from every request, scan and extension call."
	if len(res.Traces) == 0 {
		return settingsFrame(nav, sectionTraces, "Traces", lead,
			traceFilterRow(filter).Build(),
			ui.EmptyState(emptyIconSearch, "No traces recorded in the last day").Build()), nil
	}

	// The design leads with the shape of the data rather than with the list:
	// percentiles, an error rate and a throughput, then where the time actually
	// went. All of it is computed from the summaries already in hand — the store
	// is not asked a second question.
	body := make([]sdui.Node, 0, 4)
	if stats := traceStats(res.Traces); stats != nil {
		body = append(body, stats.Build())
	}
	if hist := latencyHistogram(res.Traces); hist != nil {
		body = append(body, hist.Build())
	}
	body = append(body, traceFilterRow(filter).Build())

	entries := make([]ui.El, 0, len(res.Traces))
	for _, t := range res.Traces {
		summary := fmt.Sprintf("%s · %d spans", t.StartedAt.Format("15:04:05"), t.Spans)
		if t.Errors > 0 {
			// Worth its own mention: a trace can succeed overall while something
			// inside it failed and was recovered — a search swallowing an
			// unreachable addon is exactly that, and it is invisible from the
			// outcome alone.
			summary += fmt.Sprintf(" · %d failed", t.Errors)
		}
		entries = append(entries, ui.TraceRow(t.Root,
			ui.Origin(shortID(t.Trace)),
			ui.Summary(summary),
			ui.Value(formatDuration(t.Duration)),
			ui.When(traceColor(t) != "", ui.Tone(traceColor(t))),
			ui.OnTap(ui.Navigate(screenTrace, map[string]any{paramTrace: t.Trace}))))
	}
	body = append(body, ui.Stack("vertical", 2, entries...).Build())
	return settingsFrame(nav, sectionTraces, "Traces", lead, body...), nil
}

// traceStats is the design's row of figures over the traces in hand.
//
// Percentiles over the *returned* set, which is what the screen can honestly
// claim: the query is a filtered, limited window, and a percentile presented as
// the install's is a claim about traces this screen never saw.
func traceStats(traces []domain.TelemetryTraceSummary) *ui.Element {
	if len(traces) == 0 {
		return nil
	}
	sorted := make([]time.Duration, 0, len(traces))
	failed := 0
	var earliest, latest time.Time
	for _, t := range traces {
		sorted = append(sorted, t.Duration)
		if t.Status == "error" || t.Errors > 0 {
			failed++
		}
		if earliest.IsZero() || t.StartedAt.Before(earliest) {
			earliest = t.StartedAt
		}
		if t.StartedAt.After(latest) {
			latest = t.StartedAt
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pct := func(p float64) string {
		i := int(float64(len(sorted)-1) * p)
		return formatDuration(sorted[i])
	}

	cards := []ui.El{
		ui.StatCard("P50", pct(0.50)),
		ui.StatCard("P95", pct(0.95)),
		ui.StatCard("P99", pct(0.99)),
		ui.StatCard("Errors", fmt.Sprintf("%.1f%%", 100*float64(failed)/float64(len(traces)))),
	}
	// Throughput needs a window to divide by, and one trace has none. Omitted
	// rather than shown as a rate over zero time.
	if window := latest.Sub(earliest); window >= time.Second {
		perMin := float64(len(traces)) / window.Minutes()
		cards = append(cards, ui.StatCard("Throughput", fmt.Sprintf("%.0f / min", perMin)))
	}
	return ui.Grid(ui.MinColumnWidth(130), ui.Gap(3), ui.Group(cards...))
}

// latencyHistogram buckets the durations, which is what the percentiles cannot
// show: whether the slow tail is two traces or a third of them.
//
// The bars are sized here rather than in the client because only the server
// knows what the tallest bucket is, and a bar drawn as a fraction of something
// the client guessed would be a different chart on every render.
func latencyHistogram(traces []domain.TelemetryTraceSummary) *ui.Element {
	if len(traces) < 2 {
		return nil
	}
	edges := []time.Duration{
		50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond,
		500 * time.Millisecond, time.Second,
	}
	labels := []string{"<50ms", "50–100", "100–250", "250–500", "500ms–1s", ">1s"}
	counts := make([]int, len(labels))
	for _, t := range traces {
		placed := false
		for i, e := range edges {
			if t.Duration < e {
				counts[i]++
				placed = true
				break
			}
		}
		if !placed {
			counts[len(counts)-1]++
		}
	}
	tallest := 0
	for _, c := range counts {
		if c > tallest {
			tallest = c
		}
	}
	if tallest == 0 {
		return nil
	}
	buckets := make([]any, 0, len(counts))
	for i, c := range counts {
		// A non-zero bucket always draws something: a bar rounded to nothing
		// reads as an empty bucket, which is a different fact.
		h := 0
		if c > 0 {
			h = 4 + int(44*float64(c)/float64(tallest))
		}
		buckets = append(buckets, map[string]any{
			"label": labels[i], "count": strconv.Itoa(c), "height": h,
		})
	}
	return ui.LatencyHistogram("Latency distribution",
		ui.Summary(fmt.Sprintf("%d traces · %d buckets", len(traces), len(counts))),
		ui.Buckets(buckets))
}

// traceColor is the status dot's colour, empty for a trace that ran clean.
func traceColor(t domain.TelemetryTraceSummary) string {
	switch {
	case t.Status == "error":
		return "danger"
	case t.Errors > 0:
		return "warning"
	default:
		return ""
	}
}

// traceScreen is the waterfall for one trace: the span tree with durations,
// and the log records emitted inside it.
//
// The tree is drawn by depth from the parent chain rather than by indentation
// the store computed, because the entry span's parent is the *client's* span
// and lives outside this process (ADR 0054). Anything whose parent is not in
// the result set is a root here.
func (s *Service) traceScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	nav, navErr := s.settingsNav(ctx, caller)
	if navErr != nil {
		return nil, navErr
	}
	nav.selected = true

	traceID := stringParam(params, paramTrace)
	res, err := s.content.GetTrace(ctx, app.GetTraceQuery{Caller: caller, TraceID: traceID})
	if err != nil {
		return nil, err
	}

	const lead = "Where the time went, and what the code was saying while it went there."
	if len(res.Spans) == 0 && len(res.Logs) == 0 {
		return settingsFrame(nav, sectionTraces, "Trace", lead,
			ui.EmptyState(emptyIconSearch, "Nothing is stored for that trace").Build()), nil
	}

	body := make([]sdui.Node, 0, 2)

	if len(res.Spans) > 0 {
		total := rootDuration(res.Spans)
		ordered := orderSpans(res.Spans)
		rows := make([]ui.El, 0, len(ordered))
		for _, sp := range ordered {
			rows = append(rows, spanRow(sp.span, sp.depth, total))
		}

		// The panel's header answers the three questions a waterfall is opened
		// with — what ran, which trace this is, and whether it came out all
		// right — before any of the bars are read.
		summary := fmt.Sprintf("%d %s · %s", len(res.Spans), plural(len(res.Spans), "span"),
			formatDuration(total))
		if failed := failedSpans(res.Spans); failed > 0 {
			summary += fmt.Sprintf(" · %d failed", failed)
		} else {
			summary += " · ok"
		}
		body = append(body, ui.DetailPanel(traceRootName(res.Spans),
			ui.Origin("trace="+shortID(traceID)),
			ui.Summary(summary),
			ui.Stack("vertical", 2, rows...),
		).Build())
	}

	if len(res.Logs) > 0 {
		rows := make([]any, 0, len(res.Logs))
		for _, r := range res.Logs {
			rows = append(rows, map[string]any{
				"time":    r.Time.Format("15:04:05.000"),
				"level":   r.Level,
				"tone":    logColor(r.Level),
				"service": logService(r),
				"message": r.Message,
			})
		}
		body = append(body, ui.DetailPanel("Correlated records",
			ui.Summary(strconv.Itoa(len(res.Logs))+" "+plural(len(res.Logs), "record")),
			ui.LogTable(ui.Rows(rows)),
		).Build())
	}

	return settingsFrame(nav, sectionTraces, "Trace", lead, body...), nil
}

// traceRootName is the operation a person recognises — the entry span's name.
func traceRootName(spans []domain.TelemetrySpanRecord) string {
	byID := make(map[string]bool, len(spans))
	for _, sp := range spans {
		byID[sp.Span] = true
	}
	for _, sp := range spans {
		// Anything whose parent is not in the set is a root here: the entry
		// span's parent is the *client's* span and lives outside this process
		// (ADR 0054).
		if sp.Parent == "" || !byID[sp.Parent] {
			return sp.Name
		}
	}
	return "Trace"
}

// failedSpans counts the spans that did not succeed. A trace can come out ok
// with something inside it having failed and been recovered, which is invisible
// from the outcome alone.
func failedSpans(spans []domain.TelemetrySpanRecord) int {
	n := 0
	for _, sp := range spans {
		if sp.Status == "error" {
			n++
		}
	}
	return n
}

// spanRow renders one span, indented by depth and showing its share of the
// whole. The share is the point: a waterfall exists to answer "which part of
// this was the time", and a duration alone does not.
func spanRow(sp domain.TelemetrySpanRecord, depth int, total time.Duration) ui.El {
	share := "0%"
	if total > 0 {
		pct := 100 * float64(sp.Duration) / float64(total)
		if pct > 100 {
			pct = 100
		}
		// A span that ran always draws something. A bar rounded away reads as a
		// span that took no time, which is a different claim from "too short to
		// see".
		if pct < 1 && sp.Duration > 0 {
			pct = 1
		}
		share = fmt.Sprintf("%.0f%%", pct)
	}
	tone := "accent"
	if sp.Status == "error" {
		tone = "danger"
	}
	// Depth as a space token rather than as leading whitespace in the name,
	// which is what it used to be: indentation belongs to the layout, and a name
	// padded with spaces cannot be selected or searched for.
	indent := depth
	if indent > 6 {
		indent = 6
	}
	return ui.SpanRow(sp.Name,
		ui.Depth(indent),
		ui.Share(share),
		ui.Tone(tone),
		ui.Value(formatDuration(sp.Duration)))
}

// spanAtDepth is a span and how deep it sits in the trace's tree.
type spanAtDepth struct {
	span  domain.TelemetrySpanRecord
	depth int
}

func orderSpans(spans []domain.TelemetrySpanRecord) []spanAtDepth {
	bySpan := make(map[string]domain.TelemetrySpanRecord, len(spans))
	children := make(map[string][]domain.TelemetrySpanRecord, len(spans))
	for _, sp := range spans {
		bySpan[sp.Span] = sp
	}
	var roots []domain.TelemetrySpanRecord
	for _, sp := range spans {
		// A parent outside this result set means the span is a root *here* —
		// which is the normal case for the entry span, whose parent is the
		// client's own span and was never stored by this process.
		if _, ok := bySpan[sp.Parent]; sp.Parent == "" || !ok {
			roots = append(roots, sp)
			continue
		}
		children[sp.Parent] = append(children[sp.Parent], sp)
	}

	byStart := func(s []domain.TelemetrySpanRecord) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Time.Before(s[j].Time) })
	}
	byStart(roots)
	for k := range children {
		byStart(children[k])
	}

	var out []spanAtDepth
	// Iterative rather than recursive: a malformed parent chain must not be
	// able to blow the stack, and this data can arrive from a partial retention
	// window where some parents have already been dropped.
	var walk func(sp domain.TelemetrySpanRecord, depth int)
	seen := make(map[string]bool, len(spans))
	walk = func(sp domain.TelemetrySpanRecord, depth int) {
		if seen[sp.Span] || depth > 32 {
			return
		}
		seen[sp.Span] = true
		out = append(out, spanAtDepth{span: sp, depth: depth})
		for _, child := range children[sp.Span] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	// Anything left unvisited — a cycle, or a parent dropped by retention —
	// is appended flat rather than silently omitted. A waterfall missing rows
	// is worse than one with a few unplaced ones.
	for _, sp := range spans {
		if !seen[sp.Span] {
			out = append(out, spanAtDepth{span: sp, depth: 0})
		}
	}
	return out
}

// rootDuration is the longest root span's duration — the whole operation, as
// the caller experienced it. Summing spans would double-count, since a child's
// time is inside its parent's.
func rootDuration(spans []domain.TelemetrySpanRecord) time.Duration {
	var longest time.Duration
	for _, sp := range spans {
		if sp.Duration > longest {
			longest = sp.Duration
		}
	}
	return longest
}

// levelFilterRow offers the level filters as navigations back into this screen.
func levelFilterRow(params map[string]any) *ui.Element {
	current := stringParam(params, paramLevel)
	buttons := make([]ui.El, 0, 4)
	for _, level := range []string{"debug", "info", "warn", "error"} {
		style := "chip"
		if level == current {
			style = "chipOn"
		}
		buttons = append(buttons, ui.Button(level, style,
			ui.OnTap(ui.Navigate(screenLogs, map[string]any{paramLevel: level}))))
	}
	return ui.Stack("horizontal", 2, ui.Wrap(true), ui.Group(buttons...))
}

// traceFilterRow offers the ordering and failed-only toggles.
func traceFilterRow(f domain.TelemetryTraceFilter) *ui.Element {
	recentStyle, slowestStyle, failedStyle := "chip", "chip", "chip"
	if f.Order == domain.TraceOrderSlowest {
		slowestStyle = "chipOn"
	} else {
		recentStyle = "chipOn"
	}
	if f.FailedOnly {
		failedStyle = "chipOn"
	}
	return ui.Stack("horizontal", 2, ui.Wrap(true),
		ui.Button("Recent", recentStyle,
			ui.OnTap(ui.Navigate(screenTraces, map[string]any{paramOrder: string(domain.TraceOrderRecent)}))),
		ui.Button("Slowest", slowestStyle,
			ui.OnTap(ui.Navigate(screenTraces, map[string]any{paramOrder: string(domain.TraceOrderSlowest)}))),
		ui.Button("Failed only", failedStyle,
			ui.OnTap(ui.Navigate(screenTraces, map[string]any{paramFailed: "true"}))),
	)
}

// logTone maps a level onto the skin's tone vocabulary.
func logTone(level string) string {
	switch level {
	case "error":
		return "danger"
	case "warn":
		return "warning"
	default:
		return "neutral"
	}
}

// traceTone colours a trace row by outcome, counting a recovered failure
// inside it as worth noticing.
func traceTone(t domain.TelemetryTraceSummary) string {
	switch {
	case t.Status != "ok":
		return "danger"
	case t.Errors > 0:
		return "warning"
	default:
		return "neutral"
	}
}

// renderFields flattens a record's stored field document into one readable
// line, keys sorted so two records of the same shape line up.
//
// It renders values as text and nothing else. The document can hold anything a
// module chose to record (ADR 0059), so this must not become a place where a
// value is interpreted.
func renderFields(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc) == 0 {
		return ""
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, doc[k]))
	}
	return strings.Join(parts, "  ")
}

// formatDuration renders a span duration at a resolution a human reads, rather
// than the microseconds it is stored in.
func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
}

// shortID abbreviates a trace id for a label. The full id is always in the
// navigation params, so nothing is lost.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
