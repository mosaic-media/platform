package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// HealthProbe reports component readiness and degradation (MEG-015 §03).
type HealthProbe interface {
	Check(ctx context.Context) (domain.HealthStatus, error)
}
