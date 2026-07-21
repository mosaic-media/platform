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

// GetGrantsForUserQuery reads the role grants bound to a user.
type GetGrantsForUserQuery struct {
	CallerSessionID domain.SessionID
	TargetUserID    domain.UserID
}

// GetGrantsForUserResult is the Platform result type returned by
// GetGrantsForUser.
type GetGrantsForUserResult struct {
	Grants []domain.Grant
}

func validateGetGrantsForUserQuery(query GetGrantsForUserQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.TargetUserID == "" {
		return contracts.NewError(contracts.InvalidArgument, "target user id is required")
	}
	return nil
}

// GetGrantsForUser implements the query boundary, per the command order.
func (s *Service) GetGrantsForUser(ctx context.Context, query GetGrantsForUserQuery) (GetGrantsForUserResult, error) {
	if err := validateGetGrantsForUserQuery(query); err != nil {
		return GetGrantsForUserResult{}, err
	}

	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return GetGrantsForUserResult{}, err
	}

	resource := policy.Resource{Type: "user", ID: string(query.TargetUserID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionPermissionRead, resource, policy.PolicyContext{}); err != nil {
		return GetGrantsForUserResult{}, err
	}

	grants, err := s.permissions.GrantsForUser(ctx, query.TargetUserID)
	if err != nil {
		return GetGrantsForUserResult{}, err
	}
	return GetGrantsForUserResult{Grants: grants}, nil
}
