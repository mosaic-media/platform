// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// SessionForCaller resolves a credential to the session behind it.
//
// The session transport keys its live state — the outbound mailbox, the replay
// cursor, the current route — by session, and since platform#58 the value a
// client presents is not one. An access token rotates every few minutes, so a
// live session keyed by it would be orphaned on every refresh, taking its cursor
// and its route with it.
//
// It authenticates and deliberately does not authorize. There is no action to
// gate: it answers "which session is this", a fact about the credential already
// presented. It reveals nothing either — a caller that can present a credential
// can already use it, and one that cannot gets Unauthenticated.
func (s *Service) SessionForCaller(ctx context.Context, caller v1.Caller) (domain.Session, error) {
	return s.sessionManager.Validate(ctx, s.sessionStore, s.tokens,
		domain.SessionCredential(caller.Session))
}
