package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// UserStore provides local user persistence and lookup (MEG-015 §03).
type UserStore interface {
	Create(ctx context.Context, user domain.User) (domain.User, error)
	FindByID(ctx context.Context, id domain.UserID) (domain.User, error)
	FindByUsername(ctx context.Context, username string) (domain.User, error)
	Update(ctx context.Context, user domain.User) (domain.User, error)
}
