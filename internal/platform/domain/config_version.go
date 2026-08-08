// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// ConfigStatus is a ConfigVersion's position in the activation state machine.
type ConfigStatus string

const (
	// ConfigDraft is a newly saved, unvalidated configuration candidate.
	ConfigDraft ConfigStatus = "draft"
	// ConfigValidated means schema and policy checks passed; the version may
	// be activated.
	ConfigValidated ConfigStatus = "validated"
	// ConfigPending means a user asked for this version and applying it
	// needs more than the Platform can do to itself — a restart, a new
	// Generation, or the recovery flow. It is the difference between a
	// version that merely passed validation and one somebody chose: without
	// it, a restart would either apply every validated candidate nobody
	// asked for, or apply none of them and lose the request.
	ConfigPending ConfigStatus = "pending"
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
// activation state machine (Draft -> Validated -> Active -> Superseded, with
// Validated -> Rejected as the failed-validation branch, and Validated ->
// Pending -> Active for a change that cannot be applied without escalation).
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

	// RequestedAt is when a user asked for this version, set by the
	// transition to Pending. It is distinct from ActivatedAt because the gap
	// between the two is the escalation, and how long a change has been
	// waiting for a restart is a question an operator asks.
	RequestedAt  *time.Time
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

// AwaitingEscalation reports whether this version was asked for and is
// waiting for the restart, Generation or recovery flow that can apply it.
func (c ConfigVersion) AwaitingEscalation() bool {
	return c.Status == ConfigPending
}

// MarkPending returns a copy of c transitioned to ConfigPending: requested,
// and waiting for an escalation that can apply it.
func (c ConfigVersion) MarkPending(now time.Time) ConfigVersion {
	c.Status = ConfigPending
	c.RequestedAt = &now
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
