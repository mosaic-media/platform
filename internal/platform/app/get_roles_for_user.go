package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// ActionPermissionRead is the policy action evaluated for GetRolesForUser,
// GetGrantsForUser and GetEffectivePermissions (MEG-015 §09 —
// Permissions).
const ActionPermissionRead policy.Action = "permission.read"

// GetRolesForUserQuery reads the roles granted to a user (MEG-015 §09 —
// Permissions: "roles").
type GetRolesForUserQuery struct {
	CallerSessionID domain.SessionID
	TargetUserID    domain.UserID
}

// GetRolesForUserResult is the Platform result type returned by
// GetRolesForUser.
type GetRolesForUserResult struct {
	Roles []domain.Role
}

func validateGetRolesForUserQuery(query GetRolesForUserQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.TargetUserID == "" {
		return contracts.NewError(contracts.InvalidArgument, "target user id is required")
	}
	return nil
}

// GetRolesForUser implements the query boundary from MEG-015 §04.
func (s *Service) GetRolesForUser(ctx context.Context, query GetRolesForUserQuery) (GetRolesForUserResult, error) {
	if err := validateGetRolesForUserQuery(query); err != nil {
		return GetRolesForUserResult{}, err
	}

	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return GetRolesForUserResult{}, err
	}

	resource := policy.Resource{Type: "user", ID: string(query.TargetUserID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionPermissionRead, resource, policy.PolicyContext{}); err != nil {
		return GetRolesForUserResult{}, err
	}

	roles, err := s.permissions.RolesForUser(ctx, query.TargetUserID)
	if err != nil {
		return GetRolesForUserResult{}, err
	}
	return GetRolesForUserResult{Roles: roles}, nil
}
