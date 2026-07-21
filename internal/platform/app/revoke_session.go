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
	// 1. validate command shape.
	if err := validateRevokeSessionCommand(cmd); err != nil {
		return RevokeSessionResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return RevokeSessionResult{}, err
	}

	// 3. authorize action through policy.
	resource := policy.Resource{Type: "session", ID: string(cmd.TargetSessionID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionSessionRevoke, resource, policy.PolicyContext{}); err != nil {
		return RevokeSessionResult{}, err
	}

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5. load state through contracts.
		if _, err := tx.Sessions().FindByID(ctx, cmd.TargetSessionID); err != nil {
			return err
		}

		// 6. apply domain rules: revoke the target session.
		if err := s.sessionManager.Revoke(ctx, tx.Sessions(), cmd.TargetSessionID); err != nil {
			return err
		}

		// 7. persist state and outbox events in the same transaction.
		event := domain.OutboxEvent{Event: s.newEvent("session.revoked", []byte(string(cmd.TargetSessionID)), string(callerID))}
		return tx.Outbox().Append(ctx, event)
	})
	if err != nil {
		return RevokeSessionResult{}, err
	}

	// 8. return a Platform result type.
	return RevokeSessionResult{SessionID: cmd.TargetSessionID}, nil
}
