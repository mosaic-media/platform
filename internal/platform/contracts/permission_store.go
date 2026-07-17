package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// PermissionStore provides role, grant and attribute lookup (MEG-015 §03).
type PermissionStore interface {
	RolesForUser(ctx context.Context, userID domain.UserID) ([]domain.Role, error)
	GrantsForUser(ctx context.Context, userID domain.UserID) ([]domain.Grant, error)
	AttributesForUser(ctx context.Context, userID domain.UserID) ([]domain.Attribute, error)
}
