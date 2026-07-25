// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
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
// It authorises `user.read` against the caller's own id, like every other read
// of a user. That is not obviously right — the record is the caller's own, and a
// session already proves ownership of it, so an argument exists for
// authenticating and stopping there. The boundary conformance test settles it:
// every caller-bearing entry point on this Service denies a caller with no
// grants, and carving one exception into that rule for a convenience would make
// the rule something each reader has to check rather than rely on. The cost is
// that an account holding no role at all cannot read its own name, which is a
// state the install should not be able to reach.
func (s *Service) GetCurrentUser(ctx context.Context, q GetCurrentUserQuery) (GetCurrentUserResult, error) {
	if q.Caller.Session == "" {
		return GetCurrentUserResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	az, err := s.enter(ctx, q.Caller, ActionUserRead, policy.Resource{Type: "user"})
	if err != nil {
		return GetCurrentUserResult{}, err
	}
	user, err := s.users.FindByID(ctx, az.userID)
	if err != nil {
		return GetCurrentUserResult{}, err
	}
	return GetCurrentUserResult{User: user}, nil
}
