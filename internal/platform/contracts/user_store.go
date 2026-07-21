// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// UserStore provides local user persistence and lookup.
type UserStore interface {
	Create(ctx context.Context, user domain.User) (domain.User, error)
	FindByID(ctx context.Context, id domain.UserID) (domain.User, error)
	FindByUsername(ctx context.Context, username string) (domain.User, error)
	Update(ctx context.Context, user domain.User) (domain.User, error)
	// List returns every user, ordered by CreatedAt. No pagination yet — a
	// first cut, like the rest of this contract set.
	List(ctx context.Context) ([]domain.User, error)
}
