package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
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
