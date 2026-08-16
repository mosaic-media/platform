// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// UserPreference is one setting a user chose for themselves.
//
// It is not authority. A preference decides what a user is shown; their
// permissions decide what they may reach. Expert mode is the case where that
// matters (platform#36): the toggle reveals the diagnostics surface and
// telemetry.read is what permits the data behind it, so a user who flips it
// without the grant sees a denial rather than a leak. Nothing that reads a
// preference may treat it as permission.
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

// PreferenceExpertMode reveals the diagnostics surface (platform#36).
//
// Named here rather than spelled as a string literal at each call site: a
// divergence between the Platform and the emit-side would be a toggle that
// silently never takes effect.
const PreferenceExpertMode = "ui.expert_mode"

// PreferenceHomeRows is how one viewer arranged their home screen
// (platform#59): which rows they hid, and which they put in an order of their
// own.
//
// It holds decisions, never a picture of the screen. A row nobody has decided
// about is absent from the document, so it appears; storing the full shape
// would freeze each user's home as it was when they first touched it, and a
// row added later would never reach them.
//
// It is a preference, not a scope: a hidden row stays reachable by search and
// by link, and hiding it is not an access control. Anything that genuinely
// must not be reachable is the content scope, a different mechanism that is
// unbuilt.
const PreferenceHomeRows = "ui.home.rows"

// PreferenceLanguages is which languages a viewer wants to hear and read
// (platform#83).
//
// Language belongs to a person, not to an install: four people sharing one
// library must not get one person's answer.
//
// The value is {audio: [...], subtitles: [...], subtitleMode: "..."} — two
// ordered lists and a mode. The mode says what the viewer wants when the audio
// preference was met, because a subtitle setting expressed on its own is wrong
// half the time: someone who asked for an English dub wants forced subtitles
// alongside it and the whole dialogue when no dub exists, which is one
// preference rather than two. The escalation lives in the playback decision,
// the only place that knows which case a release turned out to be.
const PreferenceLanguages = "playback.languages"
