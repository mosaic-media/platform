package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// ConfigStore provides configuration version persistence (MEG-015 §03).
type ConfigStore interface {
	// Save persists a new configuration version. It is insert-only: status
	// transitions on an existing version go through UpdateStatus.
	Save(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error)
	// Latest returns the most recently created configuration version,
	// regardless of status. It answers "what is the newest known
	// candidate", not "what is currently effective" — use FindActive for
	// that.
	Latest(ctx context.Context) (domain.ConfigVersion, error)
	FindByID(ctx context.Context, id domain.ConfigVersionID) (domain.ConfigVersion, error)
	// FindActive returns the configuration version currently in the Active
	// status (MEG-015 §08). At most one version is ever Active; NotFound is
	// returned if none has been activated yet.
	FindActive(ctx context.Context) (domain.ConfigVersion, error)
	// UpdateStatus persists a status transition (validate, activate,
	// reject, supersede) already computed by the config domain layer. It
	// overwrites the mutable transition fields of an existing version.
	UpdateStatus(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error)
}
