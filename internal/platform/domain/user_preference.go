// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// UserPreference is one setting a user chose for themselves.
//
// It is not authority. A preference decides what a user is *shown*; their
// permissions decide what they may *reach*. Expert mode is the case that makes
// the distinction load-bearing (ADR 0058): the toggle reveals the diagnostics
// surface and `telemetry.read` is what permits the data behind it, so a user
// who flips it without the grant sees a denial rather than a leak. Nothing
// that reads a preference may treat it as permission.
type UserPreference struct {
	// UserID is the person the preference belongs to.
	UserID UserID
	// Key is a dotted name, matching the config schema's shape:
	// "ui.expert_mode".
	Key string
	// Value is the preference as JSON — a boolean, a string, or a small object,
	// so a new preference needs no migration. It is stored uninterpreted; the
	// surface that reads it owns its meaning.
	Value []byte
	// UpdatedAt is when it was last written.
	UpdatedAt time.Time
}

// PreferenceExpertMode reveals the diagnostics surface (ADR 0058).
//
// Named here rather than left as a string literal at each call site so the
// Platform and the emit-side cannot drift on its spelling — the failure would
// be a toggle that silently never takes effect, which is close to the worst
// shape a bug can have in a preference.
const PreferenceExpertMode = "ui.expert_mode"
