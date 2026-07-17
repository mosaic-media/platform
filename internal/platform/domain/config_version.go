package domain

import "time"

// ConfigVersion is a persisted, immutable configuration snapshot. Activation
// is a separate step from persistence: a version can be saved and later
// activated once it passes validation (MEG-015 §08).
type ConfigVersion struct {
	ID          ConfigVersionID
	Payload     []byte
	CreatedAt   time.Time
	ActivatedAt *time.Time
}

// Activated reports whether this version has been activated.
func (c ConfigVersion) Activated() bool {
	return c.ActivatedAt != nil
}
