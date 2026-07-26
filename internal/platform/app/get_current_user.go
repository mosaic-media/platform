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
// into a name. `GetUserByID` cannot serve that — it needs the answer as its
// input.
//
// **It authenticates and deliberately does not authorise**, which is a change
// from how it was first written. It used to require `user.read`, on the
// argument that carving an exception into the boundary rule for a convenience
// would make the rule something each reader has to check rather than rely on.
// That argument was made when the only account that existed held everything,
// and the cost it accepted — "an account holding no role cannot read its own
// name" — turned out to be the ordinary case the moment a second account
// existed: `user.read` is administrator authority, so every viewer on the
// server was refused their own name and the account cluster on every screen
// drew a question mark.
//
// There is no action to gate here. The record is the caller's own, the session
// already proves that, and a permission to be told what you have just proved is
// not an access control. It joins the small exempt list beside
// SessionForCaller, which is the same shape of fact about the credential
// presented.
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
