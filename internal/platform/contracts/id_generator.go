// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import "github.com/mosaic-media/platform/internal/platform/domain"

// IDGenerator provides stable identity creation. It commits to nothing about
// the generation strategy (UUID, ULID, sequence, ...); that choice belongs
// entirely to the adapter.
type IDGenerator interface {
	// NewID returns a fresh, unique identity.
	NewID() domain.ID
}
