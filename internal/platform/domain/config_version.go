package domain

import "time"

// ConfigStatus is a ConfigVersion's position in the MEG-015 §08 activation
// state machine.
type ConfigStatus string

const (
	// ConfigDraft is a newly saved, unvalidated configuration candidate.
	ConfigDraft ConfigStatus = "draft"
	// ConfigValidated means schema and policy checks passed; the version may
	// be activated.
	ConfigValidated ConfigStatus = "validated"
	// ConfigActive means this version is the currently effective
	// configuration. At most one version is ever Active at a time.
	ConfigActive ConfigStatus = "active"
	// ConfigRejected means validation failed; the version can never be
	// activated.
	ConfigRejected ConfigStatus = "rejected"
	// ConfigSuperseded means this version was Active and a newer version has
	// since taken its place.
	ConfigSuperseded ConfigStatus = "superseded"
)

// ConfigVersion is a persisted configuration snapshot moving through the
// MEG-015 §08 activation state machine (Draft -> Validated -> Active ->
// Superseded, with Validated -> Rejected as the failed-validation branch).
// The Payload itself is immutable once saved; only Status and the
// transition timestamps below change over the version's lifetime.
type ConfigVersion struct {
	ID      ConfigVersionID
	Payload []byte
	Status  ConfigStatus

	CreatedAt time.Time

	// ValidatedAt and ValidationDetail are set by the validate transition,
	// whether it results in ConfigValidated or ConfigRejected.
	ValidatedAt      *time.Time
	ValidationDetail string

	ActivatedAt  *time.Time
	RejectedAt   *time.Time
	SupersededAt *time.Time
}

// Activated reports whether this version is the currently effective
// configuration.
func (c ConfigVersion) Activated() bool {
	return c.Status == ConfigActive
}

// CanValidate reports whether the validate transition may run against this
// version: only a Draft can be validated.
func (c ConfigVersion) CanValidate() bool {
	return c.Status == ConfigDraft
}

// CanActivate reports whether the activate transition may run against this
// version: only a Validated version can be activated.
func (c ConfigVersion) CanActivate() bool {
	return c.Status == ConfigValidated
}

// MarkValidated returns a copy of c transitioned to ConfigValidated.
func (c ConfigVersion) MarkValidated(now time.Time, detail string) ConfigVersion {
	c.Status = ConfigValidated
	c.ValidatedAt = &now
	c.ValidationDetail = detail
	return c
}

// MarkRejected returns a copy of c transitioned to ConfigRejected.
func (c ConfigVersion) MarkRejected(now time.Time, detail string) ConfigVersion {
	c.Status = ConfigRejected
	c.ValidatedAt = &now
	c.ValidationDetail = detail
	return c
}

// MarkActive returns a copy of c transitioned to ConfigActive.
func (c ConfigVersion) MarkActive(now time.Time) ConfigVersion {
	c.Status = ConfigActive
	c.ActivatedAt = &now
	return c
}

// MarkSuperseded returns a copy of c transitioned to ConfigSuperseded.
func (c ConfigVersion) MarkSuperseded(now time.Time) ConfigVersion {
	c.Status = ConfigSuperseded
	c.SupersededAt = &now
	return c
}
