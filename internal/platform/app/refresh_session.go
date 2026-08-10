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
// **Step 2 of the command boundary is the refresh token itself**, exactly as
// the password credential plays that role in AuthenticateLocalUser: there is no
// caller session to look up, because the whole point of this call is that the
// caller's access token has expired. There is deliberately no step 3 either —
// no policy action gates it, because refreshing is not a new authority, it is
// the continuation of one already granted. A user whose permissions were
// trimmed while they were away gets the same reduced set the moment they use
// the new token, which is the property that made an opaque token worth its
// store read (platform#43).
//
// The register's `refreshSession` row stops being *never worked* here.
func (s *Service) RefreshSession(ctx context.Context, cmd RefreshSessionCommand) (RefreshSessionResult, error) {
	// 1. validate command shape.
	if cmd.RefreshToken == "" {
		return RefreshSessionResult{}, contracts.NewError(contracts.Unauthenticated, "a refresh token is required")
	}
	if s.tokens == nil {
		return RefreshSessionResult{}, contracts.NewError(contracts.Unavailable, "this Platform issues no session credentials")
	}

	var result RefreshSessionResult

	// 4. open a UnitOfWork. Rotation is several writes — spending the old
	// token, storing the new pair, moving the idle clock — and a crash between
	// them would leave a client holding a token the server has spent and no
	// replacement, which is a sign-out it could not explain.
	err := s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 2-3. the refresh token authenticates; nothing further authorises.
		session, pair, err := s.sessionManager.Refresh(
			ctx, tx.Sessions(), tx.Tokens(), cmd.RefreshToken, cmd.DeviceID)
		if err != nil {
			return err
		}

		// 7. persist state and the outbox event in the same transaction.
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "session.refreshed", []byte(session.ID), string(session.UserID)),
		}); err != nil {
			return err
		}

		// Re-resolve what this account may do (platform#24). The stored set is the
		// snapshot taken when the session was issued, and a session lives ninety
		// days; a permission granted or withdrawn in that time would otherwise
		// not reach the client's affordance gate until the next sign-in, which
		// for a device that never signs out is never. Rotation happens every few
		// minutes, so this is the natural place for it, and it costs one role
		// read on a call that is already a transaction.
		//
		// The row keeps its issue-time value. Nothing reads it, the wire carries
		// the current set, and adding a store write to keep a column nobody
		// consumes in step would be work in the wrong direction.
		session.Capabilities = s.sessionCapabilities(ctx, session.UserID)

		result = RefreshSessionResult{Session: session, Tokens: pair}
		return nil
	})
	if err != nil {
		// A compromised chain must be revoked *outside* the transaction that
		// detected it, because that transaction is being rolled back: a
		// revocation written inside it would be undone by the very error
		// reporting it, and the attacker's descendant would keep working.
		//
		// This is the bug the wire test found. Everything below the seam was
		// correct and the rollback silently undid it, which is exactly the
		// shape of failure an in-memory store cannot reproduce.
		if compromise, ok := sessions.CompromiseOf(err); ok {
			if revokeErr := s.tokens.RevokeChain(ctx, compromise.ChainID, s.clock.Now()); revokeErr != nil {
				telemetry.From(ctx).For("sessions").Error(
					"a compromised refresh chain could not be revoked", telemetry.Err(revokeErr))
			} else {
				telemetry.From(ctx).For("sessions").Warn(
					"revoked a refresh chain after a replayed or misdirected token",
					telemetry.String("reason", compromise.Reason))
			}
			return RefreshSessionResult{}, contracts.NewError(contracts.Unauthenticated, compromise.Reason)
		}
		return RefreshSessionResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
