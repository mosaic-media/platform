// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// InstalledExtensionStore persists the set of extension modules a user has
// installed from a trusted repository (ADR 0081). It is Platform-owned durable
// state and default-empty: a fresh Platform composes only its core modules, and
// every row here is an extension someone chose. The Platform reads the whole set
// at boot to re-adopt each — verify then spawn — and mutates it on install and
// uninstall.
//
// It holds identity and provenance, not the binary (ADR 0012: a module owns no
// schema, so its install record is a Platform store, not a table it defines).
// This is deliberately distinct from ModuleSettingsStore: whether the Platform
// *has* a module and how a module it has is *configured* are different
// questions, and one row answering both would have to represent "configured but
// not installed" as a state.
type InstalledExtensionStore interface {
	// List returns every installed extension, ordered by module id, for boot
	// re-adoption and the settings surface. The empty set is the default and is
	// not an error.
	List(ctx context.Context) ([]domain.InstalledExtension, error)
	// Upsert records an installed extension, replacing any earlier record for the
	// same module id — a reinstall or a version change writes over the old row.
	Upsert(ctx context.Context, ext domain.InstalledExtension) (domain.InstalledExtension, error)
	// Remove deletes the record for a module id. Removing one that is not present
	// is not an error: uninstall is idempotent.
	Remove(ctx context.Context, moduleID string) error
}
