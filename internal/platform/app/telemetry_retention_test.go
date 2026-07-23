// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
)

// Retention is read from Active configuration each sweep, which is what makes
// the fields Hot (ADR 0058). The audit floor is the part that must not bend.

func TestRetentionFallsBackToDefaultsWithNoActiveConfig(t *testing.T) {
	svc, _, _, _ := importFixture(t)

	got := svc.TelemetryRetention(context.Background())
	if got.Logs != app.DefaultTelemetryRetention.Logs || got.Spans != app.DefaultTelemetryRetention.Spans {
		t.Fatalf("expected the defaults with no active config, got %+v", got)
	}
}

// TestAuditRetentionCannotBeShortenedBelowTheFloor is the security property.
// "Set audit retention to an hour, act, wait" is the standard way to defeat an
// audit log, so the floor is compile-time and not itself configurable.
func TestAuditRetentionCannotBeShortenedBelowTheFloor(t *testing.T) {
	svc, db, _, _ := importFixture(t)
	db.seedActiveConfig(t, map[string]any{
		"telemetry.retention.audit_days": 1,
		"telemetry.retention.logs_days":  3,
	})

	got := svc.TelemetryRetention(context.Background())
	if got.Audit < 30*24*time.Hour {
		t.Fatalf("audit retention was shortened below the floor: %v", got.Audit)
	}
	// The other signals have no floor — shortening them only loses
	// diagnostics, which is an operator's own business.
	if got.Logs != 3*24*time.Hour {
		t.Fatalf("log retention should honour the configured value, got %v", got.Logs)
	}
}

// TestRetentionIgnoresUnusableValues — retention governs a background sweep, so
// a typo must not become an outage while the disk fills. Zero is rejected
// specifically: it would drop the partition currently being written to.
func TestRetentionIgnoresUnusableValues(t *testing.T) {
	svc, db, _, _ := importFixture(t)
	db.seedActiveConfig(t, map[string]any{
		"telemetry.retention.logs_days":    0,
		"telemetry.retention.traces_hours": -5,
	})

	got := svc.TelemetryRetention(context.Background())
	if got.Logs != app.DefaultTelemetryRetention.Logs {
		t.Fatalf("a zero retention must be rejected, got %v", got.Logs)
	}
	if got.Spans != app.DefaultTelemetryRetention.Spans {
		t.Fatalf("a negative retention must be rejected, got %v", got.Spans)
	}
}
