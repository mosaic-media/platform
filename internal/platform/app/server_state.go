// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// ServerState is the one thing a caller with no session is told about this
// install (ADR 0101, carrying ADR 0098's two doorway states unchanged).
type ServerState struct {
	// Claimed is whether anybody owns this server yet. Unclaimed means no
	// account exists, so the doorway is setup rather than sign-in.
	Claimed bool
}

// ServerState answers which doorway to serve.
//
// **It takes no caller, and that is the whole point.** Every other method on
// this Service begins by resolving a principal; this one is reached before a
// principal can exist, so it has none to resolve — which is why it is here as
// its own method rather than as a flag on a query that authenticates. A reader
// looking for the boundary should find its absence stated rather than have to
// infer it from a missing line.
//
// What it discloses is bounded to one bit, deliberately. "Does this server have
// an owner" is a fact an unauthenticated caller learns from the doorway it is
// shown regardless — a setup screen and a sign-in screen are visibly different
// — so returning it is not a new disclosure. Anything more (how many accounts,
// what they are called, when the server was claimed) would be, and none of it is
// here.
//
// It never fails. A store error means the doorway would otherwise be nothing at
// all, and between "claimed" and "unclaimed" the safe wrong answer is claimed:
// showing a setup screen on a server that has an owner invites somebody to try
// to claim it, while showing sign-in on an unclaimed one is merely a dead end.
func (s *Service) ServerState(ctx context.Context) ServerState {
	if s.users == nil {
		return ServerState{Claimed: true}
	}
	users, err := s.users.List(ctx)
	if err != nil {
		telemetry.From(ctx).For("auth").Error(
			"could not read whether this server is claimed; serving the sign-in doorway", telemetry.Err(err))
		return ServerState{Claimed: true}
	}
	return ServerState{Claimed: len(users) > 0}
}
