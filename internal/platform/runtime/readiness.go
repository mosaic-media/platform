// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// ReadinessResult is the Supervisor-facing answer to "is activation safe
// right now".
type ReadinessResult struct {
	Ready      bool
	Components []domain.ComponentHealth
}

// CheckReadiness reports Ready=false if ANY registered component is
// Unavailable, computed from real, live component health
// (internal/platform/diagnostics) — never hardcoded true. A component that
// is merely Degraded does not block activation: component health should be
// granular enough without reducing the whole system to a single failed
// state — a degraded event bus should not stop the Supervisor from
// activating a Platform whose storage is fine.
func CheckReadiness(ctx context.Context, registry *diagnostics.Registry) ReadinessResult {
	snapshot := registry.Snapshot(ctx)
	ready := true
	for _, c := range snapshot {
		if c.Health == domain.HealthUnavailable {
			ready = false
		}
	}
	return ReadinessResult{Ready: ready, Components: snapshot}
}
