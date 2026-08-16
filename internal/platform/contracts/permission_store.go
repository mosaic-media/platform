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
	// FindRoleByName returns the role with the given name, or NotFound.
	//
	// A role's name is unique across the install, so provisioning a second
	// account from a preset must find the existing role rather than create it
	// again: "create a role called User" is a Conflict the second time, and an
	// account provisioned through that path ends up with no authority at all.
	//
	// A preset role is therefore an install-wide named role that several people
	// hold, not a per-account snapshot. The snapshot property belongs to the
	// role: a role created today keeps the set it was created with, and nothing
	// widens it afterwards except the owner reconciliation below.
	FindRoleByName(ctx context.Context, name string) (domain.Role, error)
	// FindRole returns the role with the given id, or NotFound.
	//
	// It exists for the delegation check (platform#44): granting a role must be
	// bounded by what the grantor holds, and that cannot be decided without
	// seeing what the role carries.
	FindRole(ctx context.Context, roleID domain.RoleID) (domain.Role, error)
	// GrantRole binds an existing role to an existing user. A duplicate grant
	// is Conflict; a grant naming a role or user that does not exist is
	// Conflict as well, surfaced from the foreign keys.
	GrantRole(ctx context.Context, grant domain.Grant) error
	// SetRolePermissions replaces what a role carries.
	//
	// It is not a general role editor. A preset is snapshotted into a role row
	// when that role is created, so adding an action to the Platform never
	// reaches an account that already exists. The install owner's account is
	// the one place that must not be able to drift, because it is the root of
	// every other grant: an authority it does not hold can never be given to
	// anyone. The bootstrap reconciles it on every boot; nothing else calls
	// this.
	SetRolePermissions(ctx context.Context, roleID domain.RoleID, perms []domain.Permission) error
}
