// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// ActionPermissionRead is the policy action evaluated for GetRolesForUser,
// GetGrantsForUser and GetEffectivePermissions.
const ActionPermissionRead policy.Action = "permission.read"

// GetRolesForUserQuery reads the roles granted to a user.
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

// GetRolesForUser implements the query boundary, per the command order.
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
