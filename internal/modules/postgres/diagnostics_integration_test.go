// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/events"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// TestDiagnosticsRegistryReportsRealStateAcrossPostgresWorkerAndBus is the
// Diagnostics exit criterion proven end to end: a
// diagnostics.Registry wired to the real PostgreSQL adapter, the real
// outbox Worker and the real event Bus reports genuinely different,
// live-computed health for each — not a hardcoded "ok" anywhere — and that
// snapshot survives being turned into a redacted support bundle and a
// structured log entry.
func TestDiagnosticsRegistryReportsRealStateAcrossPostgresWorkerAndBus(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	mod := postgres.Module{}
	cs := mod.Bind(pool)
	c := context.Background()

	bus := events.NewBus()
	worker := events.NewWorker(cs.Outbox, bus, "outbox-worker")

	registry := diagnostics.NewRegistry()
	registry.Register("postgres", cs.HealthReporter)
	registry.Register("event-bus", bus)
	registry.Register("outbox-worker", worker, "postgres", "event-bus")

	// Before the worker has ever run, it must report Unavailable — a real,
	// live consequence of never having drained anything, not a fake value.
	before := registry.Snapshot(c)
	byComponent := func(snap []domain.ComponentHealth) map[string]domain.ComponentHealth {
		m := make(map[string]domain.ComponentHealth, len(snap))
		for _, s := range snap {
			m[s.Component] = s
		}
		return m
	}
	beforeMap := byComponent(before)
	if beforeMap["postgres"].Health != domain.HealthHealthy {
		t.Fatalf("postgres health = %q, want %q against a real migrated database", beforeMap["postgres"].Health, domain.HealthHealthy)
	}
	if beforeMap["outbox-worker"].Health != domain.HealthUnavailable {
		t.Fatalf("outbox-worker health before any run = %q, want %q", beforeMap["outbox-worker"].Health, domain.HealthUnavailable)
	}
	// The worker's dependency health must reflect postgres's REAL state,
	// computed in the same snapshot — not a static placeholder.
	depHealth := map[string]domain.HealthState{}
	for _, dep := range beforeMap["outbox-worker"].DependencyHealth {
		depHealth[dep.Component] = dep.Health
	}
	if depHealth["postgres"] != domain.HealthHealthy {
		t.Fatalf("outbox-worker's postgres dependency health = %q, want %q", depHealth["postgres"], domain.HealthHealthy)
	}

	// Drive a real event through the real outbox, then let the real worker
	// drain it, and confirm the snapshot changes to reflect that — genuine
	// state transition, not a scripted result.
	if err := cs.Outbox.Append(c, domain.OutboxEvent{Event: domain.Event{
		ID: "e-diag-1", Type: "diagnostics.test", OccurredAt: cs.Clock.Now(), RecordedAt: cs.Clock.Now(), Payload: []byte("x"),
	}}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := worker.RunOnce(c); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	after := byComponent(registry.Snapshot(c))
	if after["outbox-worker"].Health != domain.HealthHealthy {
		t.Fatalf("outbox-worker health after a successful drain = %q, want %q", after["outbox-worker"].Health, domain.HealthHealthy)
	}
	if after["outbox-worker"].LastSuccessfulCheck.IsZero() {
		t.Fatal("expected a non-zero LastSuccessfulCheck after a real successful drain")
	}

	// The full snapshot must survive becoming a redacted support bundle and
	// a structured log entry without losing component identification.
	bundle := diagnostics.BuildSupportBundle("mosaic-platform", "v1", registry.Snapshot(c), cs.Clock.Now())
	bundlePath := filepath.Join(t.TempDir(), "bundle.json")
	if err := diagnostics.WriteSupportBundle(bundlePath, bundle); err != nil {
		t.Fatalf("WriteSupportBundle: %v", err)
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var written diagnostics.SupportBundle
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(written.Components) != 3 {
		t.Fatalf("len(written.Components) = %d, want 3", len(written.Components))
	}

	logPath := filepath.Join(t.TempDir(), "diagnostics.log")
	sink, err := telemetry.NewFileSink(logPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	defer sink.Close()
	logger := telemetry.New(sink, telemetry.Resource{ServiceName: "mosaic-platform"}, telemetry.LevelInfo)
	for _, health := range registry.Snapshot(c) {
		logger.For(health.Component).Info("health check", telemetry.ComponentHealthFields(health)...)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(log): %v", err)
	}
	if len(logData) == 0 {
		t.Fatal("expected non-empty log output")
	}
}
