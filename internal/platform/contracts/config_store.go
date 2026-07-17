package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// ConfigStore provides configuration version persistence (MEG-015 §03).
type ConfigStore interface {
	Save(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error)
	Latest(ctx context.Context) (domain.ConfigVersion, error)
	FindByID(ctx context.Context, id domain.ConfigVersionID) (domain.ConfigVersion, error)
}
