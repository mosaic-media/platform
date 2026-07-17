package runtime

import (
	"context"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/config"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// ConfigActivationStatus reports the currently active configuration
// version and its reload class (MEG-015 §10 — Config activation status).
type ConfigActivationStatus struct {
	HasActiveVersion bool
	VersionID        string
	ActivatedAt      time.Time
	// ReloadClass is always Hot for whatever IS active: config.Manager's
	// Activate (MEG-015 §08) only ever transitions a version to Active
	// when its change was Hot-classified — a Generation/Restart/Recovery
	// change is left Validated instead, precisely so it never reaches
	// here. This reports that structural invariant directly rather than
	// guessing at a value nothing persists.
	ReloadClass string
}

// CheckConfigActivation reads the active version directly from store. This
// is an internal, Supervisor-facing operational surface — like a health
// probe, not a user-facing query — so it intentionally bypasses the
// app.Service authentication/policy gate a GraphQL caller goes through.
func CheckConfigActivation(ctx context.Context, store contracts.ConfigStore) ConfigActivationStatus {
	active, err := store.FindActive(ctx)
	if err != nil {
		return ConfigActivationStatus{HasActiveVersion: false}
	}

	activatedAt := active.CreatedAt
	if active.ActivatedAt != nil {
		activatedAt = *active.ActivatedAt
	}

	return ConfigActivationStatus{
		HasActiveVersion: true,
		VersionID:        string(active.ID),
		ActivatedAt:      activatedAt,
		ReloadClass:      string(config.Hot),
	}
}
