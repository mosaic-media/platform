// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"sort"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// GetEffectivePermissionsQuery reads the flattened set of permissions a
// user holds through all of its roles.
type GetEffectivePermissionsQuery struct {
	CallerSessionID domain.SessionID
	TargetUserID    domain.UserID
}

// GetEffectivePermissionsResult is the Platform result type returned by
// GetEffectivePermissions. Permissions is deduplicated and sorted, so the
// same input always produces the same, stable order.
type GetEffectivePermissionsResult struct {
	Permissions []domain.Permission
}

func validateGetEffectivePermissionsQuery(query GetEffectivePermissionsQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.TargetUserID == "" {
		return contracts.NewError(contracts.InvalidArgument, "target user id is required")
	}
	return nil
}

// GetEffectivePermissions implements the query boundary, per the command
// order. It reuses the same role lookup policy.Engine.Authorize drives, so
// "effective permissions" always matches what would actually be allowed.
func (s *Service) GetEffectivePermissions(ctx context.Context, query GetEffectivePermissionsQuery) (GetEffectivePermissionsResult, error) {
	if err := validateGetEffectivePermissionsQuery(query); err != nil {
		return GetEffectivePermissionsResult{}, err
	}

	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return GetEffectivePermissionsResult{}, err
	}

	resource := policy.Resource{Type: "user", ID: string(query.TargetUserID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionPermissionRead, resource, policy.PolicyContext{}); err != nil {
		return GetEffectivePermissionsResult{}, err
	}

	roles, err := s.permissions.RolesForUser(ctx, query.TargetUserID)
	if err != nil {
		return GetEffectivePermissionsResult{}, err
	}

	seen := make(map[domain.Permission]struct{})
	var permissions []domain.Permission
	for _, role := range roles {
		for _, permission := range role.Permissions {
			if _, ok := seen[permission]; ok {
				continue
			}
			seen[permission] = struct{}{}
			permissions = append(permissions, permission)
		}
	}
	sort.Slice(permissions, func(i, j int) bool { return permissions[i] < permissions[j] })

	return GetEffectivePermissionsResult{Permissions: permissions}, nil
}
