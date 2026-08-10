// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// ModuleSettings is an optional module's user-managed configuration: a single
// opaque JSON document keyed by the module's id. The Platform stores it
// without interpreting it — the module owns its meaning (platform#9's
// unvalidated-JSON rule applied to configuration).
//
// It is deliberately not a ConfigVersion. Platform configuration is operator-
// owned, versioned and reload-classed; module settings are user-owned data a
// user changes freely at runtime (adding a Stremio addon by manifest URL, say),
// with no version to activate and no reload class to declare.
type ModuleSettings struct {
	// ModuleID is the capability's manifest id — the key.
	ModuleID string
	// Settings is the raw JSON document. Empty is treated as "{}".
	Settings []byte
	// UpdatedAt is when the document was last written.
	UpdatedAt time.Time
}
