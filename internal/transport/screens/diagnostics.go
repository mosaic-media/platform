// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

	rows := []ui.El{levelFilterRow(params)}
	if len(res.Records) == 0 {
		rows = append(rows, ui.EmptyState(emptyIconSearch, "No records match that filter in the last day"))
		return ui.Screen(ui.Title("Logs"), ui.Stack("vertical", 12, rows...)).Build(), nil
	}

	entries := make([]ui.El, 0, len(res.Records))
	for _, r := range res.Records {
		entries = append(entries, logRow(r))
	}
	rows = append(rows, ui.Stack("vertical", 4, entries...))
	return ui.Screen(ui.Title("Logs"), ui.Stack("vertical", 12, rows...)).Build(), nil
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
		els = append(els, ui.Button("Trace "+shortID(r.Trace), "ghost",
			ui.OnTap(ui.Navigate(screenTrace, map[string]any{paramTrace: r.Trace}))))
	}
	return ui.Stack("vertical", 2, els...)
}

// tracesScreen lists recent traces: what ran, how long it took, and whether
// anything inside it failed.
func (s *Service) tracesScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	filter := domain.TelemetryTraceFilter{Order: domain.TraceOrderRecent}
	if stringParam(params, paramOrder) == string(domain.TraceOrderSlowest) {
		filter.Order = domain.TraceOrderSlowest
	}
	filter.FailedOnly = stringParam(params, paramFailed) == "true"

	res, err := s.content.ListTraces(ctx, app.ListTracesQuery{Caller: caller, Filter: filter})
	if err != nil {
		return nil, err
	}

	rows := []ui.El{traceFilterRow(filter)}
	if len(res.Traces) == 0 {
		rows = append(rows, ui.EmptyState(emptyIconSearch, "No traces recorded in the last day"))
		return ui.Screen(ui.Title("Traces"), ui.Stack("vertical", 12, rows...)).Build(), nil
	}

	entries := make([]ui.El, 0, len(res.Traces))
	for _, t := range res.Traces {
		meta := fmt.Sprintf("%s · %s · %d spans", t.StartedAt.Format("15:04:05"), formatDuration(t.Duration), t.Spans)
		if t.Errors > 0 {
			// Worth its own mention: a trace can succeed overall while
			// something inside it failed and was recovered — a search
			// swallowing an unreachable addon is exactly that, and it is
			// invisible from the outcome alone.
			meta += fmt.Sprintf(" · %d failed", t.Errors)
		}
		entries = append(entries, ui.Stack("vertical", 2,
			ui.Button(t.Root, "ghost", ui.OnTap(ui.Navigate(screenTrace, map[string]any{paramTrace: t.Trace}))),
			ui.Badge(meta, traceTone(t)),
		))
	}
	rows = append(rows, ui.Stack("vertical", 6, entries...))
	return ui.Screen(ui.Title("Traces"), ui.Stack("vertical", 12, rows...)).Build(), nil
}

// traceScreen is the waterfall for one trace: the span tree with durations,
// and the log records emitted inside it.
//
// The tree is drawn by depth from the parent chain rather than by indentation
// the store computed, because the entry span's parent is the *client's* span
// and lives outside this process (ADR 0054). Anything whose parent is not in
// the result set is a root here.
func (s *Service) traceScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	traceID := stringParam(params, paramTrace)
	res, err := s.content.GetTrace(ctx, app.GetTraceQuery{Caller: caller, TraceID: traceID})
	if err != nil {
		return nil, err
	}

	rows := []ui.El{ui.Badge("trace "+traceID, "neutral")}

	if len(res.Spans) > 0 {
		total := rootDuration(res.Spans)
		spans := make([]ui.El, 0, len(res.Spans))
		for _, sp := range orderSpans(res.Spans) {
			spans = append(spans, spanRow(sp.span, sp.depth, total))
		}
		rows = append(rows, ui.Section("Waterfall", ui.Stack("vertical", 2, spans...)))
	}

	if len(res.Logs) > 0 {
		logs := make([]ui.El, 0, len(res.Logs))
		for _, r := range res.Logs {
			logs = append(logs, ui.Stack("vertical", 2,
				ui.Badge(r.Time.Format("15:04:05.000")+" · "+r.Level+" · "+r.Component, logTone(r.Level)),
				ui.Subtitle(r.Message)))
		}
		rows = append(rows, ui.Section("Records", ui.Stack("vertical", 4, logs...)))
	}

	return ui.Screen(ui.Title("Trace"), ui.Stack("vertical", 12, rows...)).Build(), nil
}

// spanRow renders one span, indented by depth and showing its share of the
// whole. The share is the point: a waterfall exists to answer "which part of
// this was the time", and a duration alone does not.
func spanRow(sp domain.TelemetrySpanRecord, depth int, total time.Duration) ui.El {
	name := strings.Repeat("    ", depth) + sp.Name
	meta := formatDuration(sp.Duration)
	if total > 0 {
		meta += fmt.Sprintf(" · %d%%", int(sp.Duration*100/total))
	}
	if sp.Module != "" {
		meta += " · module:" + sp.Module
	}
	tone := "neutral"
	if sp.Status != "ok" {
		tone = "danger"
		if sp.ErrorCategory != "" {
			meta += " · " + sp.ErrorCategory
		}
	}
	return ui.Stack("vertical", 1, ui.Subtitle(name), ui.Badge(meta, tone))
}

// spanAtDepth pairs a span with its depth in the tree.
type spanAtDepth struct {
	span  domain.TelemetrySpanRecord
	depth int
}

// orderSpans arranges spans parent-before-child, depth-first, so the rendered
// list reads as a tree.
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
func levelFilterRow(params map[string]any) ui.El {
	current := stringParam(params, paramLevel)
	buttons := make([]ui.El, 0, 4)
	for _, level := range []string{"debug", "info", "warn", "error"} {
		style := "ghost"
		if level == current {
			style = "secondary"
		}
		buttons = append(buttons, ui.Button(level, style,
			ui.OnTap(ui.Navigate(screenLogs, map[string]any{paramLevel: level}))))
	}
	return ui.Stack("horizontal", 6, buttons...)
}

// traceFilterRow offers the ordering and failed-only toggles.
func traceFilterRow(f domain.TelemetryTraceFilter) ui.El {
	recentStyle, slowestStyle, failedStyle := "ghost", "ghost", "ghost"
	if f.Order == domain.TraceOrderSlowest {
		slowestStyle = "secondary"
	} else {
		recentStyle = "secondary"
	}
	if f.FailedOnly {
		failedStyle = "secondary"
	}
	return ui.Stack("horizontal", 6,
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
