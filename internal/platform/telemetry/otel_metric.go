// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"context"
	"sort"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Metrics (sdk#9), and the one thing about them that is not like a log
// record or a span.
//
// **A metric is state, not an event.** A record and a span are produced, written
// once and aged out under retention (platform#36); a counter is a running total
// that has no moment of production and nothing to age. That difference decides
// the shape of everything here: there is no sink, because there is nothing to
// write when a module calls Count — there is a *reader*, asked for the current
// values at the moment somebody looks.
//
// So the destination is a `ManualReader`: no background goroutine, no export
// interval, no queue that can drop, and a snapshot that is by construction the
// values as of the question rather than as of the last flush.
//
// **The limit, stated rather than discovered:** these live in this process and
// reset when it restarts. Nothing is retained across a Generation, and a
// counter's history is not recoverable — what a reader gets is the total since
// boot. That is a genuine gap against a stored time series, and it is a
// deliberate first step rather than a design: sdk#5 declined to publish a
// metric surface the Platform could not back at all, and "backed, in memory,
// readable now" is what closes that. A retained series is a schema, a retention
// policy and a rollup, and it is a decision to take on its own evidence.

// MetricCollector owns the meter provider and answers for its current values.
//
// The composition root builds one and installs it; nothing else constructs one,
// which is the same ownership the sinks have (platform#31). A module reaches a
// `metric.Meter` and never this — a Meter records, and this is what decides
// where the recording goes.
type MetricCollector struct {
	reader   *sdkmetric.ManualReader
	provider *sdkmetric.MeterProvider

	mu sync.Mutex
	// series counts the distinct (instrument, dimensions) pairs each scope has
	// created, and warned records whether a scope has already been told it hit
	// the cap. Both are process-lifetime, because a series is.
	series map[string]map[string]struct{}
	warned map[string]bool
}

// NewMetricCollector builds a collector whose values are read on demand.
func NewMetricCollector() *MetricCollector {
	reader := sdkmetric.NewManualReader()
	return &MetricCollector{
		reader:   reader,
		provider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		series:   map[string]map[string]struct{}{},
		warned:   map[string]bool{},
	}
}

// MetricSeriesPerScope bounds how many distinct series one scope may create.
//
// **This is the metric-shaped version of sdk#5's record quota, and it needs a
// different lifetime.** A record quota is per invocation, because a chatty
// module should degrade its own call and nothing else. A series is not consumed
// by an invocation — it is created once and lives as long as the process — so a
// per-invocation cap would reset and admit the same unbounded growth on the next
// call. This one is per scope, for the life of the process.
//
// The number is generous against any reasonable instrument set and small against
// the thing being prevented: a module counting per title, or per search term,
// reaches it in an afternoon and would otherwise grow until the host runs out of
// memory — with nothing failing, because a metric never errors.
const MetricSeriesPerScope = 256

// overflowDimension is what a refused series is folded into.
//
// **Folded rather than dropped, and that distinction is the whole design.**
// Dropping the observation would make a counter under-report, so the number a
// person reads would be quietly wrong — the worst available outcome for a
// measurement. Folding keeps every total exact and loses only the breakdown,
// and the dimension says on its face that a breakdown was lost.
const overflowDimension = "mosaic.metric.overflow"

// Admit reports whether a scope may create one more series.
//
// The second return says whether this is the *first* refusal for the scope, so
// the caller records it once rather than once per call — a module in a loop
// would otherwise turn the diagnostic into the flood the cap exists to prevent,
// which is the same reasoning the record quota's overflow warning follows.
func (c *MetricCollector) Admit(scope, key string) (allowed, firstRefusal bool) {
	if c == nil {
		return true, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	known := c.series[scope]
	if known == nil {
		known = map[string]struct{}{}
		c.series[scope] = known
	}
	if _, seen := known[key]; seen {
		// Already counted. Re-admitting a series that exists is free — the cap
		// is on how many exist, not on how often they are written.
		return true, false
	}
	if len(known) >= MetricSeriesPerScope {
		first := !c.warned[scope]
		c.warned[scope] = true
		return false, first
	}
	known[key] = struct{}{}
	return true, false
}

// Meter returns the meter for one instrumentation scope.
//
// Scope is how a series is attributed to whoever recorded it — the module id for
// a module's own instruments — so attribution is stamped by the caller of this
// rather than claimed in an instrument name a module chooses (sdk#5).
func (c *MetricCollector) Meter(scope string) metric.Meter {
	if c == nil {
		return nil
	}
	return c.provider.Meter(scope)
}

// Count adds delta to a counter in one scope.
//
// **The whole operation lives here rather than in the caller**, for the reason
// the rest of this package exists: `app` calls `telemetry.Start` rather than
// reaching for a tracer, and it calls this rather than reaching for a meter. The
// cap, the fold and the OpenTelemetry types stay on one side of a line, and the
// module adapter above stays a translation of the SDK's vocabulary into this
// one.
//
// It returns whether this call is the *first* to be folded into the overflow
// dimension, so the caller records that once rather than per call, and any error
// creating the instrument — which happens only when a name is one OpenTelemetry
// refuses.
func (c *MetricCollector) Count(scope, name string, delta int64, fields []Field) (firstOverflow bool, err error) {
	if c == nil {
		return false, nil
	}
	attrs, firstOverflow := c.admitted(scope, name, fields)
	counter, err := c.provider.Meter(scope).Int64Counter(name)
	if err != nil {
		return firstOverflow, err
	}
	counter.Add(metricContext, delta, metric.WithAttributes(attrs...))
	return firstOverflow, nil
}

// Measure records one observation into a histogram in one scope.
func (c *MetricCollector) Measure(scope, name string, value float64, unit string, fields []Field) (firstOverflow bool, err error) {
	if c == nil {
		return false, nil
	}
	attrs, firstOverflow := c.admitted(scope, name, fields)
	hist, err := c.provider.Meter(scope).Float64Histogram(name, metric.WithUnit(unit))
	if err != nil {
		return firstOverflow, err
	}
	hist.Record(metricContext, value, metric.WithAttributes(attrs...))
	return firstOverflow, nil
}

// metricContext is what an instrument is written with.
//
// It is deliberately not a request context. OpenTelemetry takes one here to
// attach an *exemplar* — a link from a bucket back to the trace that landed in
// it — and the ManualReader this collector is built on exports no exemplars, so
// passing a live context would buy nothing and would tie a process-lifetime
// series to a cancellable scope. If exemplars are ever wanted, this is the line
// that has to change, and the reader is what has to change first.
var metricContext = context.Background()

// admitted converts fields to attributes and applies the series cap.
func (c *MetricCollector) admitted(scope, name string, fields []Field) ([]attribute.KeyValue, bool) {
	attrs := metricAttributes(fields)
	key := name + "\x00" + renderDimensions(attribute.NewSet(attrs...))
	allowed, firstRefusal := c.Admit(scope, key)
	if allowed {
		return attrs, false
	}
	return []attribute.KeyValue{attribute.Bool(overflowDimension, true)}, firstRefusal
}

// metricAttributes renders fields as attributes, re-applying redaction on the
// way out exactly as a sink does (platform#34), so a Field built as a struct
// literal fails closed here too.
func metricAttributes(fields []Field) []attribute.KeyValue {
	if len(fields) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(fields))
	for _, f := range fields {
		out = append(out, attributeOf(f))
	}
	return out
}

// Snapshot reads the current value of every series.
func (c *MetricCollector) Snapshot(ctx context.Context) ([]MetricSeries, error) {
	if c == nil {
		return nil, nil
	}
	var collected metricdata.ResourceMetrics
	if err := c.reader.Collect(ctx, &collected); err != nil {
		return nil, err
	}

	var out []MetricSeries
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			out = append(out, seriesOf(scope.Scope.Name, m)...)
		}
	}
	// Sorted, because a screen that reorders itself between refreshes is one
	// nobody can read a change out of. Scope first, then instrument, then the
	// rendered dimensions — the order a person scans them in.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Instrument != out[j].Instrument {
			return out[i].Instrument < out[j].Instrument
		}
		return out[i].Dimensions < out[j].Dimensions
	})
	return out, nil
}

// Shutdown releases the provider.
func (c *MetricCollector) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.provider.Shutdown(ctx)
}

// MetricSeries is one instrument's value at one set of dimensions.
//
// It is flattened deliberately. A counter and a histogram are different shapes
// — a total against a distribution — and a reader wants them in one list,
// ordered together, rather than in two sections that have to be cross-read. Kind
// says which fields mean anything.
type MetricSeries struct {
	// Scope is who recorded it: a module id for a module's instruments.
	Scope string
	// Instrument is the name the caller gave.
	Instrument string
	// Unit is the annotation, empty when unitless.
	Unit string
	// Kind is "counter" or "histogram".
	Kind string
	// Dimensions renders the attribute set, sorted, as `k=v, k=v`. Rendered
	// rather than structured because the only consumer is a reader: the values
	// are already redacted (platform#34), and a map would be reassembled into
	// exactly this string by whatever displayed it.
	Dimensions string
	// Total is a counter's accumulated value, or a histogram's sum.
	Total float64
	// Count is a histogram's observation count. Zero for a counter.
	Count uint64
	// Min and Max bound a histogram's observations. Both zero for a counter.
	Min float64
	Max float64
}

// MetricKind values, so a reader and a screen agree on the two words.
const (
	MetricKindCounter   = "counter"
	MetricKindHistogram = "histogram"
)

// seriesOf flattens one instrument's data points.
//
// Only the two aggregations the Platform creates are handled. A third — a gauge,
// an exponential histogram — would be produced by an instrument nothing here
// constructs, so it is skipped rather than guessed at: rendering a shape this
// code does not understand as though it were a counter is worse than not
// rendering it, in exactly the way sdk#6's artwork slots are.
func seriesOf(scope string, m metricdata.Metrics) []MetricSeries {
	var out []MetricSeries
	switch data := m.Data.(type) {
	case metricdata.Sum[int64]:
		for _, point := range data.DataPoints {
			out = append(out, MetricSeries{
				Scope: scope, Instrument: m.Name, Unit: m.Unit,
				Kind:       MetricKindCounter,
				Dimensions: renderDimensions(point.Attributes),
				Total:      float64(point.Value),
			})
		}
	case metricdata.Sum[float64]:
		for _, point := range data.DataPoints {
			out = append(out, MetricSeries{
				Scope: scope, Instrument: m.Name, Unit: m.Unit,
				Kind:       MetricKindCounter,
				Dimensions: renderDimensions(point.Attributes),
				Total:      point.Value,
			})
		}
	case metricdata.Histogram[float64]:
		for _, point := range data.DataPoints {
			series := MetricSeries{
				Scope: scope, Instrument: m.Name, Unit: m.Unit,
				Kind:       MetricKindHistogram,
				Dimensions: renderDimensions(point.Attributes),
				Total:      point.Sum,
				Count:      point.Count,
			}
			// Min and Max are optional in the data model — an aggregation may
			// legitimately not track them — so they are read through their
			// accessors rather than assumed present.
			if v, ok := point.Min.Value(); ok {
				series.Min = v
			}
			if v, ok := point.Max.Value(); ok {
				series.Max = v
			}
			out = append(out, series)
		}
	}
	return out
}

// renderDimensions writes an attribute set as sorted `k=v` pairs.
func renderDimensions(set attribute.Set) string {
	if set.Len() == 0 {
		return ""
	}
	pairs := make([]string, 0, set.Len())
	for _, kv := range set.ToSlice() {
		pairs = append(pairs, string(kv.Key)+"="+kv.Value.Emit())
	}
	sort.Strings(pairs)
	rendered := pairs[0]
	for _, p := range pairs[1:] {
		rendered += ", " + p
	}
	return rendered
}

// meterKey carries the collector through the context, the same ambient rule the
// logger and the span sink already follow (platform#31).
type meterKey struct{}

// WithMetrics returns a context whose instruments reach collector.
func WithMetrics(ctx context.Context, collector *MetricCollector) context.Context {
	if collector == nil {
		return ctx
	}
	return context.WithValue(ctx, meterKey{}, collector)
}

// MetricsFrom returns the configured collector, or nil.
//
// Nil is a usable answer here, unlike the logger's no-op: `MetricCollector`'s
// own methods tolerate a nil receiver, and `Meter` returns a nil `metric.Meter`
// which the SDK's TelemetryOptions already treats as "discard". A caller
// therefore needs no check, and an unconfigured process records nothing rather
// than failing.
func MetricsFrom(ctx context.Context) *MetricCollector {
	if ctx == nil {
		return nil
	}
	if c, ok := ctx.Value(meterKey{}).(*MetricCollector); ok {
		return c
	}
	return nil
}
