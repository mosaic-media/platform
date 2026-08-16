// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/sessions"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// RefreshSessionCommand exchanges a refresh token for a new pair (platform#58).
//
// The device is named as well as the token because the token is bound to it: a
// refresh presented from a different device is a stolen credential being used,
// not a client that moved.
type RefreshSessionCommand struct {
	RefreshToken string
	DeviceID     domain.DeviceID
}

// RefreshSessionResult carries the session and the pair that now spends it.
type RefreshSessionResult struct {
	Session domain.Session
	Tokens  domain.TokenPair
}

// RefreshSession rotates a session's credential pair.
//
// Step 2 of the command boundary is the refresh token itself, exactly as the
// password credential plays that role in AuthenticateLocalUser: there is no
// caller session to look up, because the point of this call is that the caller's
// access token has expired. There is deliberately no step 3 either — no policy
// action gates it, because refreshing continues an authority already granted. A
// user whose permissions were trimmed while they were away gets the reduced set
// the moment they use the new token, which is what made an opaque token worth
// its store read (platform#43).
func (s *Service) RefreshSession(ctx context.Context, cmd RefreshSessionCommand) (RefreshSessionResult, error) {
	if cmd.RefreshToken == "" {
		return RefreshSessionResult{}, contracts.NewError(contracts.Unauthenticated, "a refresh token is required")
	}
	if s.tokens == nil {
		return RefreshSessionResult{}, contracts.NewError(contracts.Unavailable, "this Platform issues no session credentials")
	}

	var result RefreshSessionResult

	// Rotation is several writes — spending the old token, storing the new pair,
	// moving the idle clock — and a crash between them would leave a client
	// holding a token the server has spent and no replacement, which is a
	// sign-out it could not explain.
	err := s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		var err error
		result, err = s.rotateSessionWithin(ctx, tx, cmd)
		return err
	})
	if err != nil {
		if compromise, ok := sessions.CompromiseOf(err); ok {
			return RefreshSessionResult{}, s.revokeCompromisedChain(ctx, compromise)
		}
		return RefreshSessionResult{}, err
	}

	return result, nil
}

// rotateSessionWithin spends the presented refresh token for a new pair and
// records the rotation, in one transaction.
func (s *Service) rotateSessionWithin(ctx context.Context, tx contracts.Tx, cmd RefreshSessionCommand) (RefreshSessionResult, error) {
	session, pair, err := s.sessionManager.Refresh(
		ctx, tx.Sessions(), tx.Tokens(), cmd.RefreshToken, cmd.DeviceID)
	if err != nil {
		return RefreshSessionResult{}, err
	}

	if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
		Event: s.newEvent(ctx, "session.refreshed", []byte(session.ID), string(session.UserID)),
	}); err != nil {
		return RefreshSessionResult{}, err
	}

	// Re-resolve what this account may do (platform#24). The stored set is the
	// snapshot taken when the session was issued and a session lives ninety days,
	// so a permission granted or withdrawn in that time would otherwise not reach
	// the client's affordance gate until the next sign-in — which for a device
	// that never signs out is never.
	//
	// The row keeps its issue-time value: nothing reads it, and the wire carries
	// the current set.
	session.Capabilities = s.sessionCapabilities(ctx, session.UserID)

	return RefreshSessionResult{Session: session, Tokens: pair}, nil
}

// revokeCompromisedChain ends the chain a replayed or misdirected token exposed,
// and refuses the caller either way. It must run outside the transaction that
// detected the compromise, because that transaction is being rolled back: a
// revocation written inside it is undone by the error reporting it, and the
// attacker's descendant keeps working. An in-memory store cannot reproduce that
// failure.
func (s *Service) revokeCompromisedChain(ctx context.Context, compromise sessions.Compromise) error {
	if revokeErr := s.tokens.RevokeChain(ctx, compromise.ChainID, s.clock.Now()); revokeErr != nil {
		telemetry.From(ctx).For("sessions").Error(
			"a compromised refresh chain could not be revoked", telemetry.Err(revokeErr))
	} else {
		telemetry.From(ctx).For("sessions").Warn(
			"revoked a refresh chain after a replayed or misdirected token",
			telemetry.String("reason", compromise.Reason))
	}
	return contracts.NewError(contracts.Unauthenticated, compromise.Reason)
}
