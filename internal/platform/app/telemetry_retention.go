// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"
	"time"
)

// Retention field names, matching config.PlatformSchema. Named here rather than
// written as literals at the read site so the schema and the reader cannot
// drift on a spelling — the failure would be a configured value that silently
// never takes effect, which is the worst shape this kind of bug has.
const (
	fieldRetentionLogsDays   = "telemetry.retention.logs_days"
	fieldRetentionTracesHrs  = "telemetry.retention.traces_hours"
	fieldRetentionAuditDays  = "telemetry.retention.audit_days"
	fieldRetentionMetricDays = "telemetry.retention.metrics_days"
)

// TelemetryRetention is how long each signal is kept.
type TelemetryRetention struct {
	Logs   time.Duration
	Spans  time.Duration
	Audit  time.Duration
	Metric time.Duration
}

// auditRetentionFloor is the shortest audit retention this Platform will honour,
// whatever the Active configuration says (ADR 0057).
//
// It is a compile-time constant on purpose. "Set audit retention to an hour, do
// the thing, wait" is the standard way to defeat an audit log, so the floor must
// not itself be configurable — a floor an operator can lower is not a floor. The
// other three signals have no floor: shortening them loses diagnostics, which is
// an operator's own business.
const auditRetentionFloor = 30 * 24 * time.Hour

// DefaultTelemetryRetention is what applies when the Active configuration says
// nothing — which is the normal state, since there is no admin surface for
// drafting configuration yet.
var DefaultTelemetryRetention = TelemetryRetention{
	Logs:   14 * 24 * time.Hour,
	Spans:  72 * time.Hour,
	Audit:  400 * 24 * time.Hour,
	Metric: 30 * 24 * time.Hour,
}

// TelemetryRetention reads the retention policy from the Active configuration,
// falling back to the defaults for anything unset or unreadable.
//
// It never fails. Retention governs a background sweep, and a Platform that
// refused to start — or a sweep that refused to run — because a config value
// was malformed would turn a typo into an outage while the disk fills. An
// unusable value takes the default and the caller can say so.
func (s *Service) TelemetryRetention(ctx context.Context) (out TelemetryRetention) {
	out = DefaultTelemetryRetention
	// A *named* result, so the deferred floor below actually reaches the
	// caller. With a local it does not: defer can only mutate a named return,
	// and the earlier version silently returned whatever configuration asked
	// for — including an audit retention of one day. The test is what caught
	// it, which is the argument for testing a floor rather than trusting it.
	defer func() {
		// Applied last, so it holds regardless of where the value came from
		// and regardless of which return path was taken.
		if out.Audit < auditRetentionFloor {
			out.Audit = auditRetentionFloor
		}
	}()

	if s.configStore == nil {
		return out
	}
	version, err := s.configStore.FindActive(ctx)
	if err != nil || len(version.Payload) == 0 {
		return out
	}
	var payload map[string]any
	if err := json.Unmarshal(version.Payload, &payload); err != nil {
		return out
	}

	if d, ok := durationField(payload, fieldRetentionLogsDays, 24*time.Hour); ok {
		out.Logs = d
	}
	if d, ok := durationField(payload, fieldRetentionTracesHrs, time.Hour); ok {
		out.Spans = d
	}
	if d, ok := durationField(payload, fieldRetentionAuditDays, 24*time.Hour); ok {
		out.Audit = d
	}
	if d, ok := durationField(payload, fieldRetentionMetricDays, 24*time.Hour); ok {
		out.Metric = d
	}
	return out
}

// durationField reads a numeric config field and multiplies it by unit,
// reporting whether it was present and usable.
//
// A negative or zero value is rejected rather than honoured: zero retention
// would mean the sweep drops every partition the moment it is created,
// including the one being written to.
func durationField(payload map[string]any, key string, unit time.Duration) (time.Duration, bool) {
	raw, ok := payload[key]
	if !ok {
		return 0, false
	}
	// JSON numbers decode to float64; a string is accepted too, since a config
	// payload is hand-edited today and "14" is an easy thing to write.
	var n float64
	switch v := raw.(type) {
	case float64:
		n = v
	case string:
		if err := json.Unmarshal([]byte(v), &n); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if n <= 0 {
		return 0, false
	}
	return time.Duration(n) * unit, true
}
