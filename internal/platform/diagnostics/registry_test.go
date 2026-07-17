package diagnostics_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/platform/diagnostics"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

type fakeReporter struct {
	health domain.ComponentHealth
}

func (f fakeReporter) ReportHealth(context.Context) domain.ComponentHealth {
	return f.health
}

func TestRegistrySnapshotReturnsEveryComponentIndependently(t *testing.T) {
	registry := diagnostics.NewRegistry()
	registry.Register("postgres", fakeReporter{health: domain.ComponentHealth{Component: "postgres", Health: domain.HealthHealthy}})
	registry.Register("event-bus", fakeReporter{health: domain.ComponentHealth{Component: "event-bus", Health: domain.HealthDegraded, DegradedReason: "one subscriber failing"}})

	snapshot := registry.Snapshot(context.Background())
	if len(snapshot) != 2 {
		t.Fatalf("len(snapshot) = %d, want 2", len(snapshot))
	}

	byComponent := map[string]domain.ComponentHealth{}
	for _, s := range snapshot {
		byComponent[s.Component] = s
	}
	if byComponent["postgres"].Health != domain.HealthHealthy {
		t.Fatalf("postgres health = %q, want %q", byComponent["postgres"].Health, domain.HealthHealthy)
	}
	if byComponent["event-bus"].Health != domain.HealthDegraded {
		t.Fatalf("event-bus health = %q, want %q", byComponent["event-bus"].Health, domain.HealthDegraded)
	}
	// One degraded component must not be reduced to a single failed state
	// for the whole system (MEG-015 §09 — Diagnostics Model).
	if byComponent["postgres"].Health == byComponent["event-bus"].Health {
		t.Fatal("expected postgres and event-bus to report independently different health")
	}
}

func TestRegistrySnapshotFillsInDependencyHealth(t *testing.T) {
	registry := diagnostics.NewRegistry()
	registry.Register("postgres", fakeReporter{health: domain.ComponentHealth{Component: "postgres", Health: domain.HealthUnavailable}})
	registry.Register("outbox-worker",
		fakeReporter{health: domain.ComponentHealth{Component: "outbox-worker", Health: domain.HealthHealthy}},
		"postgres", "event-bus", // event-bus deliberately never registered
	)

	snapshot := registry.Snapshot(context.Background())
	var worker domain.ComponentHealth
	for _, s := range snapshot {
		if s.Component == "outbox-worker" {
			worker = s
		}
	}
	if len(worker.DependencyHealth) != 2 {
		t.Fatalf("len(DependencyHealth) = %d, want 2", len(worker.DependencyHealth))
	}
	deps := map[string]domain.HealthState{}
	for _, d := range worker.DependencyHealth {
		deps[d.Component] = d.Health
	}
	if deps["postgres"] != domain.HealthUnavailable {
		t.Fatalf("postgres dependency health = %q, want %q", deps["postgres"], domain.HealthUnavailable)
	}
	// An unregistered dependency must not be silently omitted or reported
	// as falsely healthy.
	if deps["event-bus"] != domain.HealthUnavailable {
		t.Fatalf("unregistered dependency health = %q, want %q", deps["event-bus"], domain.HealthUnavailable)
	}
}

func TestRegistryComponentsReturnsSortedNames(t *testing.T) {
	registry := diagnostics.NewRegistry()
	registry.Register("event-bus", fakeReporter{})
	registry.Register("postgres", fakeReporter{})
	registry.Register("outbox-worker", fakeReporter{})

	got := registry.Components()
	want := []string{"event-bus", "outbox-worker", "postgres"}
	if len(got) != len(want) {
		t.Fatalf("Components() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Components() = %v, want %v", got, want)
		}
	}
}
