// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// permissionStore is the PostgreSQL contracts.PermissionStore. It backs the
// policy engine's RBAC lookups.
type permissionStore struct {
	q queryer
}

// NewPermissionStore builds a pool-backed PermissionStore for the policy
// engine's direct read path.
func NewPermissionStore(pool *pgxpool.Pool) contracts.PermissionStore {
	return &permissionStore{q: pool}
}

func (s *permissionStore) RolesForUser(ctx context.Context, userID domain.UserID) ([]domain.Role, error) {
	rows, err := s.q.Query(ctx,
		`SELECT r.id, r.name, r.permissions
		   FROM roles r
		   JOIN grants g ON g.role_id = r.id
		  WHERE g.user_id = $1
		  ORDER BY r.name`,
		string(userID),
	)
	if err != nil {
		return nil, mapError("query roles for user", err)
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		var (
			id          string
			name        string
			permissions []string
		)
		if err := rows.Scan(&id, &name, &permissions); err != nil {
			return nil, mapError("scan role", err)
		}
		roles = append(roles, domain.Role{
			ID:          domain.RoleID(id),
			Name:        name,
			Permissions: stringsToPermissions(permissions),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate roles", err)
	}
	return roles, nil
}

func (s *permissionStore) GrantsForUser(ctx context.Context, userID domain.UserID) ([]domain.Grant, error) {
	rows, err := s.q.Query(ctx,
		`SELECT user_id, role_id FROM grants WHERE user_id = $1 ORDER BY role_id`,
		string(userID),
	)
	if err != nil {
		return nil, mapError("query grants for user", err)
	}
	defer rows.Close()

	var grants []domain.Grant
	for rows.Next() {
		var uid, rid string
		if err := rows.Scan(&uid, &rid); err != nil {
			return nil, mapError("scan grant", err)
		}
		grants = append(grants, domain.Grant{UserID: domain.UserID(uid), RoleID: domain.RoleID(rid)})
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate grants", err)
	}
	return grants, nil
}

// FindRole returns one role, or NotFound. It backs the delegation check
// (ADR 0069), which has to see a role's permissions before deciding whether the
// grantor may hand them out.
func (s *permissionStore) FindRole(ctx context.Context, roleID domain.RoleID) (domain.Role, error) {
	row := s.q.QueryRow(ctx, `SELECT id, name, permissions FROM roles WHERE id = $1`, string(roleID))
	var role domain.Role
	if err := row.Scan(&role.ID, &role.Name, &role.Permissions); err != nil {
		if isNoRows(err) {
			return domain.Role{}, contracts.NewError(contracts.NotFound, "no role with that id")
		}
		return domain.Role{}, mapError("find role", err)
	}
	return role, nil
}

func (s *permissionStore) CreateRole(ctx context.Context, role domain.Role) (domain.Role, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO roles (id, name, permissions) VALUES ($1, $2, $3)`,
		string(role.ID), role.Name, permissionsToStrings(role.Permissions),
	)
	if err != nil {
		return domain.Role{}, mapError("create role", err)
	}
	return role, nil
}

func (s *permissionStore) SetRolePermissions(ctx context.Context, roleID domain.RoleID, perms []domain.Permission) error {
	tag, err := s.q.Exec(ctx,
		`UPDATE roles SET permissions = $2 WHERE id = $1`,
		string(roleID), permissionsToStrings(perms),
	)
	if err != nil {
		return mapError("set role permissions", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "role not found")
	}
	return nil
}

func (s *permissionStore) GrantRole(ctx context.Context, grant domain.Grant) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO grants (user_id, role_id) VALUES ($1, $2)`,
		string(grant.UserID), string(grant.RoleID),
	)
	if err != nil {
		return mapError("grant role", err)
	}
	return nil
}

func (s *permissionStore) AttributesForUser(ctx context.Context, userID domain.UserID) ([]domain.Attribute, error) {
	rows, err := s.q.Query(ctx,
		`SELECT key, value FROM resource_attributes WHERE subject_user_id = $1 ORDER BY key`,
		string(userID),
	)
	if err != nil {
		return nil, mapError("query attributes for user", err)
	}
	defer rows.Close()

	var attributes []domain.Attribute
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, mapError("scan attribute", err)
		}
		attributes = append(attributes, domain.Attribute{Key: key, Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate attributes", err)
	}
	return attributes, nil
}
