// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// ServerState is the one thing a caller with no session is told about this
// install (platform#57, carrying platform#54's two doorway states unchanged).
type ServerState struct {
	// Claimed is whether anybody owns this server yet. Unclaimed means no
	// account exists, so the doorway is setup rather than sign-in.
	Claimed bool
	// Name is what the household called this server, empty until it has been
	// named. It is read from the durable file rather than the database
	// (platform#54), which is the one fact here that survives PostgreSQL being
	// down.
	//
	// Disclosing it to an unauthenticated caller is deliberate and bounded: a
	// server name tells a person they are looking at the right door. It is the
	// only thing about the install that is disclosed — no account count, no
	// usernames, no version — and it must stay that way.
	Name string
	// Degraded reports that the claimed answer below is a fallback rather than a
	// reading. It exists so the doorway can say "I cannot reach my database"
	// instead of drawing a sign-in form that will refuse every attempt.
	Degraded bool
}

// Claimable reports whether a claim may proceed. It is the one place the
// fallback matters: a store that could not be read answers Claimed, so an
// unreadable database can never be mistaken for an empty one.
func (s ServerState) Claimable() bool { return !s.Claimed && !s.Degraded }

// ServerState answers which doorway to serve.
//
// It takes no caller. Every other method on this Service begins by resolving a
// principal; this one is reached before a principal can exist, which is why it
// is its own method rather than a flag on a query that authenticates.
//
// What it discloses is bounded to one bit, deliberately. "Does this server have
// an owner" is a fact an unauthenticated caller learns from the doorway it is
// shown regardless, since a setup screen and a sign-in screen are visibly
// different. Anything more — how many accounts, what they are called, when the
// server was claimed — would be a new disclosure and must not be added.
//
// It never fails. Between "claimed" and "unclaimed" the safe wrong answer is
// claimed: showing a setup screen on a server that has an owner invites somebody
// to try to claim it, while showing sign-in on an unclaimed one is a dead end.
func (s *Service) ServerState(ctx context.Context) ServerState {
	state := ServerState{Name: s.serverName(ctx)}
	if s.users == nil {
		state.Claimed, state.Degraded = true, true
		return state
	}
	users, err := s.users.List(ctx)
	if err != nil {
		telemetry.From(ctx).For("auth").Error(
			"could not read whether this server is claimed; serving the sign-in doorway", telemetry.Err(err))
		state.Claimed, state.Degraded = true, true
		return state
	}
	state.Claimed = len(users) > 0
	return state
}

// serverName reads the durable identity file, empty when there is none or it
// cannot be read.
//
// It never fails, like the state above: this is the one call made before a
// client can do anything, and an unnamed door is a door. A Platform built
// without an identity store — a test service, a deployment with no writable path
// — is the ordinary case of "there is no name", not an error.
func (s *Service) serverName(ctx context.Context) string {
	if s.instance == nil {
		return ""
	}
	identity, err := s.instance.Read(ctx)
	if err != nil {
		if contracts.CategoryOf(err) != contracts.NotFound {
			telemetry.From(ctx).For("auth").Warn(
				"could not read this server's name; the doorway will not show one", telemetry.Err(err))
		}
		return ""
	}
	return identity.Name
}
