// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// InstalledExtension is the durable record that a user installed one extension
// module from a trusted repository (ADR 0081). It is the Platform's source of
// truth for what to bring up at boot: the identity and provenance, not the
// bytes. The verified binary on disk is a cache re-acquirable from this record,
// so the durable truth is "this module, from this repository, at this version,
// verified against this key" — enough to re-adopt without asking anyone.
type InstalledExtension struct {
	// ModuleID is the module's manifest id — the key. It is the id the running
	// binary reports, not the repository it came from (the two differ:
	// module-stremio-addons publishes a module whose id is "stremio").
	ModuleID string
	// Repository is the trusted repository the module was installed from, kept
	// as provenance and as where a re-fetch goes when the on-disk cache is gone.
	Repository string
	// Version is the catalogued version that was installed. A later catalogued
	// version does not change this until the user acts on it — updating an
	// installed extension is a decision ADR 0081 leaves open, not a silent boot
	// upgrade.
	Version string
	// SignedBy is the trusted publisher key that verified the module at install,
	// kept as provenance for the settings surface and for an admin looking at a
	// broken import (ADR 0065): a consent clicked months ago is not context; this
	// is.
	SignedBy string
	// InstalledAt is when the user installed it.
	InstalledAt time.Time
}
