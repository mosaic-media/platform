// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// healthProbe is the PostgreSQL contracts.HealthProbe. It reports the
// storage component's readiness: reachable and fully migrated is healthy;
// reachable but behind on migrations is degraded; unreachable or
// schema-incompatible is unavailable.
type healthProbe struct {
	pool      *pgxpool.Pool
	component string
}

// NewHealthProbe builds a HealthProbe over pool.
func NewHealthProbe(pool *pgxpool.Pool) contracts.HealthProbe {
	return &healthProbe{pool: pool, component: "postgres"}
}

// Check reports the storage component's readiness as a point-in-time status:
// healthy when reachable and fully migrated, degraded when behind on
// migrations, unavailable when unreachable or schema-incompatible.
func (h *healthProbe) Check(ctx context.Context) (domain.HealthStatus, error) {
	now := time.Now().UTC()

	if err := h.pool.Ping(ctx); err != nil {
		return domain.HealthStatus{
			Component: h.component,
			State:     domain.HealthUnavailable,
			Detail:    "database unreachable",
			CheckedAt: now,
		}, nil
	}

	status, err := Status(ctx, h.pool)
	if err != nil {
		// A migration incompatibility (checksum mismatch, database-ahead,
		// partial application) surfaces here: reachable, but not usable.
		return domain.HealthStatus{
			Component: h.component,
			State:     domain.HealthUnavailable,
			Detail:    err.Error(),
			CheckedAt: now,
		}, nil
	}

	if !status.FullyMigrated() {
		return domain.HealthStatus{
			Component: h.component,
			State:     domain.HealthDegraded,
			Detail: fmt.Sprintf("schema at version %d, expected %d (%d pending)",
				status.AppliedVersion, status.LatestVersion, status.Pending),
			CheckedAt: now,
		}, nil
	}

	return domain.HealthStatus{
		Component: h.component,
		State:     domain.HealthHealthy,
		Detail:    fmt.Sprintf("schema at version %d", status.AppliedVersion),
		CheckedAt: now,
	}, nil
}

// componentHealthReporter adapts healthProbe into the richer
// contracts.ComponentHealthReporter the diagnostics model requires. It is
// stateful — unlike healthProbe.Check, which is a pure point-in-time check —
// because ReportHealth must answer "when did this component last succeed",
// which requires remembering the outcome of the previous call.
type componentHealthReporter struct {
	probe     *healthProbe
	component string

	mu                  sync.Mutex
	lastSuccessfulCheck time.Time
}

// NewComponentHealthReporter builds a ComponentHealthReporter over pool.
func NewComponentHealthReporter(pool *pgxpool.Pool) contracts.ComponentHealthReporter {
	return &componentHealthReporter{probe: &healthProbe{pool: pool, component: "postgres"}, component: "postgres"}
}

// ReportHealth never fails: Check's own error return is always nil today
// (every failure mode is already encoded as a HealthUnavailable
// domain.HealthStatus), so there is nothing for this method to report
// beyond translating that status into the richer shape.
func (r *componentHealthReporter) ReportHealth(ctx context.Context) domain.ComponentHealth {
	status, _ := r.probe.Check(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	if status.State == domain.HealthHealthy {
		r.lastSuccessfulCheck = status.CheckedAt
	}

	var lastFailureCategory string
	if status.State != domain.HealthHealthy {
		lastFailureCategory = string(contracts.Unavailable)
	}

	return domain.ComponentHealth{
		Component:           r.component,
		Lifecycle:           domain.LifecycleRunning,
		Health:              status.State,
		DegradedReason:      status.Detail,
		LastSuccessfulCheck: r.lastSuccessfulCheck,
		LastFailureCategory: lastFailureCategory,
		// Detail can include a schema-version mismatch description or a
		// migration error message — internal operational detail, not a
		// secret, but not assumed safe for a support bundle either.
		RedactionClass: domain.RedactionSensitive,
	}
}
