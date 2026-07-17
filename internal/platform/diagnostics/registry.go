package diagnostics

import (
	"context"
	"sort"
	"sync"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// registration is one component's reporter plus the names of the other
// registered components it depends on.
type registration struct {
	reporter     contracts.ComponentHealthReporter
	dependencies []string
}

// Registry aggregates real component health from every registered
// contracts.ComponentHealthReporter (MEG-015 §09 — Diagnostics Model). It
// is the mechanism that answers "granular enough... without reducing the
// whole system to a single failed state": Snapshot reports every
// component's own health independently, so one degraded component never
// masks another's.
type Registry struct {
	mu   sync.Mutex
	regs map[string]registration
	// order preserves registration order so Snapshot's output is
	// deterministic across calls, which matters for tests and for a stable
	// admin UI display order.
	order []string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{regs: make(map[string]registration)}
}

// Register adds component's reporter to the Registry. dependencies names
// other components (by the same identifier they themselves register
// under) that component depends on — Snapshot fills in DependencyHealth
// from each dependency's own just-computed health, so no reporter needs a
// direct reference to another's.
func (r *Registry) Register(component string, reporter contracts.ComponentHealthReporter, dependencies ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.regs[component]; !exists {
		r.order = append(r.order, component)
	}
	r.regs[component] = registration{reporter: reporter, dependencies: append([]string(nil), dependencies...)}
}

// Snapshot computes ComponentHealth for every registered component. Each
// reporter is called independently — one reporter panicking or hanging is
// this method's problem to isolate in a later slice (Supervisor handoff),
// not something Snapshot silently tolerates today — and DependencyHealth
// is filled in from the other components' health computed in this same
// call, so it always reflects a single consistent point in time.
func (r *Registry) Snapshot(ctx context.Context) []domain.ComponentHealth {
	r.mu.Lock()
	order := append([]string(nil), r.order...)
	regs := make(map[string]registration, len(r.regs))
	for k, v := range r.regs {
		regs[k] = v
	}
	r.mu.Unlock()

	reports := make([]domain.ComponentHealth, len(order))
	healthByComponent := make(map[string]domain.HealthState, len(order))
	for i, component := range order {
		report := regs[component].reporter.ReportHealth(ctx)
		reports[i] = report
		healthByComponent[component] = report.Health
	}

	for i, component := range order {
		deps := regs[component].dependencies
		if len(deps) == 0 {
			continue
		}
		dependencyHealth := make([]domain.ComponentDependency, 0, len(deps))
		for _, dep := range deps {
			health, known := healthByComponent[dep]
			if !known {
				health = domain.HealthUnavailable
			}
			dependencyHealth = append(dependencyHealth, domain.ComponentDependency{Component: dep, Health: health})
		}
		reports[i].DependencyHealth = dependencyHealth
	}

	return reports
}

// Components returns the registered component identifiers, sorted, for
// callers that want a stable listing without a full Snapshot.
func (r *Registry) Components() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.regs))
	for name := range r.regs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
