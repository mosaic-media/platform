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
// It exists because the session transport keys its live state — the outbound
// mailbox, the replay cursor, the current route — by *session*, and since
// ADR 0102 the value a client presents is not one. An access token rotates
// every few minutes; a live session keyed by it would be orphaned on every
// refresh, taking its cursor and its route with it, and the client would see a
// reconnect it did not ask for each time its credential turned over.
//
// **It authenticates and deliberately does not authorize.** There is no action
// to gate: it answers "which session is this", which is a fact about the
// credential already presented rather than a new thing to be permitted. It
// reveals nothing either — a caller that can present a credential can already
// use it, and a caller that cannot gets Unauthenticated, which is the answer
// every other entry point gives them too.
func (s *Service) SessionForCaller(ctx context.Context, caller v1.Caller) (domain.Session, error) {
	return s.sessionManager.Validate(ctx, s.sessionStore, s.tokens,
		domain.SessionCredential(caller.Session))
}
