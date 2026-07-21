// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// ModuleSettingsStore persists one settings document per optional module,
// keyed by module id. It is Platform-owned: a module owns no schema (ADR 0012),
// so its user-managed settings live in a Platform store the module reads
// through the invocation, never a table the module defines.
type ModuleSettingsStore interface {
	// Get returns the settings for a module. A module with no settings yet
	// returns an empty document ("{}"), not NotFound, so callers need not
	// special-case first use.
	Get(ctx context.Context, moduleID string) (domain.ModuleSettings, error)
	// Set upserts the settings document for a module and returns the stored
	// value.
	Set(ctx context.Context, settings domain.ModuleSettings) (domain.ModuleSettings, error)
}
