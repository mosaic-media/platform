// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// ConfigActivationStatus reports the currently active configuration
// version and its reload class.
type ConfigActivationStatus struct {
	HasActiveVersion bool
	VersionID        string
	ActivatedAt      time.Time
	// PendingVersionID and PendingReloadClass describe a version a user
	// asked for that this process cannot apply to itself. They are what the
	// Supervisor reads to know an escalation is owed (ADR 0120's front door
	// is how it is reached, ADR 0004 is why it is the Supervisor's job):
	// empty means nothing is waiting.
	PendingVersionID   string
	PendingReloadClass string
	// ReloadClass is always Hot for whatever IS active: config.Manager's
	// Activate only ever transitions a version to Active
	// when its change was Hot-classified — a Generation/Restart/Recovery
	// change is left Validated instead, precisely so it never reaches
	// here. This reports that structural invariant directly rather than
	// guessing at a value nothing persists.
	ReloadClass string
}

// CheckConfigActivation reads the active version directly from store. This
// is an internal, Supervisor-facing operational surface — like a health
// probe, not a user-facing query — so it intentionally bypasses the
// app.Service authentication/policy gate a client caller goes through.
func CheckConfigActivation(ctx context.Context, store contracts.ConfigStore) ConfigActivationStatus {
	active, err := store.FindActive(ctx)
	if err != nil {
		// No active version yet, but there may still be one waiting: a fresh
		// install whose first configuration needs a restart is exactly that.
		status := ConfigActivationStatus{HasActiveVersion: false}
		addPending(ctx, store, &status)
		return status
	}

	activatedAt := active.CreatedAt
	if active.ActivatedAt != nil {
		activatedAt = *active.ActivatedAt
	}

	status := ConfigActivationStatus{
		HasActiveVersion: true,
		VersionID:        string(active.ID),
		ActivatedAt:      activatedAt,
		ReloadClass:      string(config.Hot),
	}
	addPending(ctx, store, &status)
	return status
}

// addPending fills in the version waiting for an escalation, if any.
//
// The class is recomputed from the diff rather than read back, for the reason
// config.Manager.ApplyPending recomputes it: it is a function of what is
// Active now, and a value stored when the request was made could have been
// invalidated by an activation since.
func addPending(ctx context.Context, store contracts.ConfigStore, status *ConfigActivationStatus) {
	pending, err := store.FindPending(ctx)
	if err != nil {
		// Including NotFound, which is the ordinary case: nothing is waiting.
		return
	}
	status.PendingVersionID = string(pending.ID)
	status.PendingReloadClass = string(config.RequiredClassBetween(activePayload(ctx, store), pending.Payload))
}

// activePayload returns the currently active payload, or nil when there is
// none — a fresh install, where every field of the pending version counts as
// changed.
func activePayload(ctx context.Context, store contracts.ConfigStore) []byte {
	active, err := store.FindActive(ctx)
	if err != nil {
		return nil
	}
	return active.Payload
}
