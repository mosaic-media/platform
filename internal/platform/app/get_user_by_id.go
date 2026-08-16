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

// ActionUserRead is the policy action evaluated for GetUserByID.
const ActionUserRead policy.Action = "user.read"

// GetUserByIDQuery reads a single user by ID.
type GetUserByIDQuery struct {
	CallerSessionID domain.SessionID
	UserID          domain.UserID
}

// GetUserByIDResult is the Platform result type returned by GetUserByID.
type GetUserByIDResult struct {
	User domain.User
}

func validateGetUserByIDQuery(query GetUserByIDQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.UserID == "" {
		return contracts.NewError(contracts.InvalidArgument, "user id is required")
	}
	return nil
}

// GetUserByID implements the query boundary, per the command order: queries
// use a direct read contract rather than a UnitOfWork, but must still
// authenticate and pass through policy before reading state.
func (s *Service) GetUserByID(ctx context.Context, query GetUserByIDQuery) (GetUserByIDResult, error) {
	if err := validateGetUserByIDQuery(query); err != nil {
		return GetUserByIDResult{}, err
	}

	if _, err := s.enterSession(ctx, query.CallerSessionID, ActionUserRead,
		policy.Resource{Type: "user", ID: string(query.UserID)}); err != nil {
		return GetUserByIDResult{}, err
	}

	user, err := s.users.FindByID(ctx, query.UserID)
	if err != nil {
		return GetUserByIDResult{}, err
	}

	return GetUserByIDResult{User: user}, nil
}
