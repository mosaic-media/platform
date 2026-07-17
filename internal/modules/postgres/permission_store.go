package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// permissionStore is the PostgreSQL contracts.PermissionStore. It backs the
// policy engine's RBAC lookups (MEG-009 §04).
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
