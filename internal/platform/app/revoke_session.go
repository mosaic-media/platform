// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// ActionSessionRevoke is the policy action evaluated for RevokeSession.
const ActionSessionRevoke policy.Action = "user.session.revoke"

// RevokeSessionCommand revokes a session server-side: remote sign-out revokes
// server-side session records rather than relying on clients deleting tokens.
//
// CallerSessionID carries the caller's access token since platform#58, not a
// session id; TargetSessionID is a real session id, which is what a device list
// names and is not itself a credential. The field names predate the pair — see
// the note on Service.enterSession.
type RevokeSessionCommand struct {
	CallerSessionID domain.SessionID
	TargetSessionID domain.SessionID
}

// RevokeSessionResult is the Platform result type returned once the
// target session has been revoked.
type RevokeSessionResult struct {
	SessionID domain.SessionID
}

func validateRevokeSessionCommand(cmd RevokeSessionCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.TargetSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "target session id is required")
	}
	return nil
}

// RevokeSession follows the command order for server-side session revocation.
func (s *Service) RevokeSession(ctx context.Context, cmd RevokeSessionCommand) (RevokeSessionResult, error) {
	if err := validateRevokeSessionCommand(cmd); err != nil {
		return RevokeSessionResult{}, err
	}

	// Authentication only: authorisation is deferred to inside the transaction,
	// because it depends on whose session is being ended and that cannot be known
	// before the target is read. This is the one command whose steps 2 and 3 are
	// separated.
	userID, err := s.authenticate(ctx, domain.SessionCredential(cmd.CallerSessionID))
	if err != nil {
		return RevokeSessionResult{}, err
	}
	az := authorized{userID: userID}

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		return s.revokeSessionWithin(ctx, tx, az, cmd.TargetSessionID)
	})
	if err != nil {
		return RevokeSessionResult{}, err
	}

	return RevokeSessionResult{SessionID: cmd.TargetSessionID}, nil
}

// revokeSessionWithin loads the target session, authorises against its owner and
// revokes it with every credential behind it, in one transaction.
//
// The refresh chain ends here rather than when an access token happens to expire
// (platform#58), so signing out takes effect at once.
func (s *Service) revokeSessionWithin(ctx context.Context, tx contracts.Tx, az authorized, targetSessionID domain.SessionID) error {
	target, err := tx.Sessions().FindByID(ctx, targetSessionID)
	if err != nil {
		return err
	}

	if err := s.authorizeRevocation(ctx, az, target, targetSessionID); err != nil {
		return err
	}

	if err := s.sessionManager.Revoke(ctx, tx.Sessions(), tx.Tokens(), targetSessionID); err != nil {
		return err
	}

	event := domain.OutboxEvent{Event: s.newEvent(ctx, "session.revoked", []byte(string(targetSessionID)), string(az.userID))}
	return tx.Outbox().Append(ctx, event)
}

// authorizeRevocation makes owning the target stand in for the grant, and
// requires user.session.revoke to end anybody else's session.
//
// Ending your own session is signing out, which cannot be a privilege — an
// account that could not do it is one nobody can hand a shared television back
// from. Ending somebody else's is an administrative act.
func (s *Service) authorizeRevocation(ctx context.Context, az authorized, target domain.Session, targetSessionID domain.SessionID) error {
	if target.UserID != az.userID {
		subject := policy.Subject{UserID: az.userID}
		return s.authorize(ctx, subject, ActionSessionRevoke,
			policy.Resource{Type: "session", ID: string(targetSessionID)},
			policy.PolicyContext{})
	}
	return nil
}
