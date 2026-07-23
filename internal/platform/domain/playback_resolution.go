// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// PlaybackResolution is one cached answer to "where are this release's bytes,
// for a client that can decode this" (ADR 0049).
//
// It is the *perishable* half of the durable/perishable split. The candidate it
// belongs to — the release, its codecs, its size — is a Part and never expires.
// What expires is this: a debrid link whose lifetime has no contract, because it
// dies either on the provider's own schedule or the moment its torrent leaves
// the provider's cache, whichever comes first. Nothing here may be trusted on a
// clock; it is corrected when it fails.
type PlaybackResolution struct {
	// PartID is the release this resolves. It is half the key, and the durable
	// half: the Part outlives any number of resolutions of it.
	PartID string
	// CapabilityClass is the digest of what the asking client can decode
	// (ADR 0049). It is the other half of the key, and the reason a phone and a
	// television do not share an answer.
	//
	// Not a user and not a device. A resolved URL is a property of the bytes and
	// the screen; keying it by person would store one identical value per person,
	// and keying it by device would store one per identical device.
	CapabilityClass string

	// URL is where the bytes are, right now, as far as anyone last knew.
	URL string
	// Headers are what that URL's origin requires, nil when it can be fetched
	// bare. They travel with the URL because they are as perishable as it is —
	// a re-resolve replaces both or neither.
	Headers map[string]string

	// ResolvedAt is when the source last answered. It is a diagnostic and a hint
	// for how eagerly to refresh, never a correctness input: an entry two minutes
	// old can be dead and one two days old can be live.
	ResolvedAt time.Time
}
