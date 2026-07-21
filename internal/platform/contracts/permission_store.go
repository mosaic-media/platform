// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// PermissionStore provides role and grant persistence, plus attribute lookup.
// The lookups back the policy engine's RBAC decisions; the
// writes are how an administrator gives a user authority — the first path by
// which authority is assigned through the Platform rather than seeded out of
// band.
type PermissionStore interface {
	RolesForUser(ctx context.Context, userID domain.UserID) ([]domain.Role, error)
	GrantsForUser(ctx context.Context, userID domain.UserID) ([]domain.Grant, error)
	AttributesForUser(ctx context.Context, userID domain.UserID) ([]domain.Attribute, error)

	// CreateRole persists a role and the permissions it carries. A duplicate
	// id is Conflict.
	CreateRole(ctx context.Context, role domain.Role) (domain.Role, error)
	// GrantRole binds an existing role to an existing user. A duplicate grant
	// is Conflict; a grant naming a role or user that does not exist is
	// Conflict as well, surfaced from the foreign keys.
	GrantRole(ctx context.Context, grant domain.Grant) error
}
