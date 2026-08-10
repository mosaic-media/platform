// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ListSessionsQuery reads the devices a caller is signed in on.
type ListSessionsQuery struct {
	Caller v1.Caller
}

// ListSessionsResult is the caller's live sessions, most recently used first,
// with the one they are asking through marked.
type ListSessionsResult struct {
	Sessions []domain.Session
	// Current is the session the caller's own credential resolves to, so a
	// device list can say "this one" rather than showing a viewer four rows
	// and leaving them to work out which is the screen in front of them.
	Current domain.SessionID
}

// ListSessions returns the caller's own live sessions (platform#58).
//
// This is what makes a bearer pair defensible rather than merely convenient: a
// long-lived credential a user cannot see and cannot end is a long-lived
// credential, and the device id has been on the session since the beginning
// precisely so this could exist.
//
// **It answers about the caller and nobody else.** There is no target-user
// parameter, so it cannot be turned into "show me somebody else's devices" by
// a caller who holds the permission for their own — reading another account's
// sessions belongs with user administration and is a different authority. It
// authorises `preference.read`, the action every account already holds for
// reading its own things, because that is what this is.
func (s *Service) ListSessions(ctx context.Context, q ListSessionsQuery) (ListSessionsResult, error) {
	az, err := s.enter(ctx, q.Caller, ActionPreferenceRead, policy.Resource{Type: "session"})
	if err != nil {
		return ListSessionsResult{}, err
	}

	sessions, err := s.sessionStore.ListForUser(ctx, az.userID, s.clock.Now())
	if err != nil {
		return ListSessionsResult{}, err
	}

	// Which of them is this one. Resolved through the same validation an
	// ordinary call runs rather than by trusting the caller's string, so a
	// credential that has since been revoked marks nothing rather than marking
	// a row it no longer owns.
	var current domain.SessionID
	if session, err := s.sessionManager.Validate(ctx, s.sessionStore, s.tokens,
		domain.SessionCredential(q.Caller.Session)); err == nil {
		current = session.ID
	}

	return ListSessionsResult{Sessions: sessions, Current: current}, nil
}
