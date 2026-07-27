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

// PreferenceHomeRows is how one viewer arranged their home screen (ADR 0103):
// which rows they hid, and which they put in an order of their own.
//
// **It holds decisions, never a picture of the screen.** A row nobody has
// decided about is absent from the document, so it appears — which is the trap
// role presets fell into, where an account created before an action existed
// never gained it. Freezing each user's home at the shape it had when they
// first touched it would be the same mistake in a second place.
//
// It is a *preference*, not a scope: a hidden row stays reachable by search and
// by link, and nothing about hiding it is an access control. Anything that
// genuinely must not be reachable is the content scope, which is a different
// mechanism and is unbuilt.
const PreferenceHomeRows = "ui.home.rows"
