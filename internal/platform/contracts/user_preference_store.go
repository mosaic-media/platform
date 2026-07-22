// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// UserPreferenceStore persists the settings a user chose for themselves, one
// row per (user, key).
//
// Per key rather than one document per user, which is where it differs from
// ModuleSettingsStore deliberately: setting one preference would otherwise be
// a read-modify-write over all of them, which races when a user has two
// devices open, and asking "which users enabled this" would mean scanning JSON
// instead of using an index.
type UserPreferenceStore interface {
	// Get returns one preference. A key a user has never set returns
	// NotFound — unlike module settings, which default to an empty document,
	// because "unset" and "set to the zero value" are different answers for a
	// preference and a caller usually has a default of its own to fall back on.
	Get(ctx context.Context, userID domain.UserID, key string) (domain.UserPreference, error)
	// List returns every preference a user has set, ordered by key.
	List(ctx context.Context, userID domain.UserID) ([]domain.UserPreference, error)
	// Set upserts one preference and returns the stored value.
	Set(ctx context.Context, pref domain.UserPreference) (domain.UserPreference, error)
	// Delete removes one preference, returning NotFound if it was not set.
	// Deleting is meaningfully different from setting a falsy value: it
	// restores whatever default the reading surface applies, rather than
	// pinning the user to today's default forever.
	Delete(ctx context.Context, userID domain.UserID, key string) error
}
