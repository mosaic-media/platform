// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// GetCurrentUserQuery asks who the caller is.
type GetCurrentUserQuery struct {
	Caller v1.Caller
}

// GetCurrentUserResult carries the caller's own user record.
type GetCurrentUserResult struct {
	User domain.User
}

// GetCurrentUser resolves the caller's session to the user behind it.
//
// Every other read of a user takes the id of the user to read, which is right
// for an administrator looking at somebody else and useless for the one surface
// that wants to say "you": a settings screen has a session and no way to turn it
// into a name. GetUserByID cannot serve that — it needs the answer as its input.
//
// It authenticates and deliberately does not authorise. There is no action to
// gate: the record is the caller's own and the session already proves it. Do not
// gate it on user.read, which is administrator authority — that refuses every
// ordinary viewer their own name. It joins the exempt list in
// boundary_conformance_test.go beside SessionForCaller.
func (s *Service) GetCurrentUser(ctx context.Context, q GetCurrentUserQuery) (GetCurrentUserResult, error) {
	if q.Caller.Session == "" {
		return GetCurrentUserResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	userID, err := s.authenticateCaller(ctx, q.Caller)
	if err != nil {
		return GetCurrentUserResult{}, err
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return GetCurrentUserResult{}, err
	}
	return GetCurrentUserResult{User: user}, nil
}
