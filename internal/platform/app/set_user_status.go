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

// ActionUserStatusUpdate is the policy action evaluated for SetUserStatus.
const ActionUserStatusUpdate policy.Action = "user.status.update"

// SetUserStatusCommand sets a target user's admin-managed status.
// CallerSessionID must belong to an authorized administrator, not the
// target user, matching CreateLocalUser's shape.
type SetUserStatusCommand struct {
	CallerSessionID domain.SessionID
	TargetUserID    domain.UserID
	Status          domain.UserStatus
}

// SetUserStatusResult is the Platform result type returned once the
// status change has committed.
type SetUserStatusResult struct {
	User domain.User
}

func validateSetUserStatusCommand(cmd SetUserStatusCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.TargetUserID == "" {
		return contracts.NewError(contracts.InvalidArgument, "target user id is required")
	}
	switch cmd.Status {
	case domain.UserActive, domain.UserSuspended:
	default:
		return contracts.NewError(contracts.InvalidArgument, "status must be active or suspended")
	}
	return nil
}

// SetUserStatus implements the command boundary.
func (s *Service) SetUserStatus(ctx context.Context, cmd SetUserStatusCommand) (SetUserStatusResult, error) {
	// 1. validate command shape.
	if err := validateSetUserStatusCommand(cmd); err != nil {
		return SetUserStatusResult{}, err
	}

	// 2-3. authenticate the caller and authorize the action.
	az, err := s.enterSession(ctx, cmd.CallerSessionID, ActionUserStatusUpdate,
		policy.Resource{Type: "user", ID: string(cmd.TargetUserID)})
	if err != nil {
		return SetUserStatusResult{}, err
	}

	var result SetUserStatusResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5. load state through contracts.
		target, err := tx.Users().FindByID(ctx, cmd.TargetUserID)
		if err != nil {
			return err
		}

		// 6. apply the domain rule: set the new status.
		target.Status = cmd.Status
		target.UpdatedAt = s.clock.Now()

		// 7. persist state and outbox events in the same transaction.
		updated, err := tx.Users().Update(ctx, target)
		if err != nil {
			return err
		}
		// Suspending ends the account's live sessions, in the same transaction.
		// This is what makes suspension take effect: without it the status
		// column is written and nothing reads it, so a suspended account can
		// sign in and one already signed in stays signed in for ninety days.
		//
		// Here rather than checked on every request: a per-request status check
		// would add a user read to a path that already does two, and closes the
		// same window. Reactivating deliberately does not restore the sessions
		// — a device that was signed out signs in again, and resurrecting
		// credentials somebody was told had ended should not be accidental.
		if cmd.Status == domain.UserSuspended {
			live, err := tx.Sessions().ListForUser(ctx, cmd.TargetUserID, s.clock.Now())
			if err != nil {
				return err
			}
			for _, session := range live {
				if err := s.sessionManager.Revoke(ctx, tx.Sessions(), tx.Tokens(), session.ID); err != nil {
					return err
				}
			}
		}

		event := domain.OutboxEvent{Event: s.newEvent(ctx, "user.status_changed", []byte(string(cmd.Status)), string(az.userID))}
		if err := tx.Outbox().Append(ctx, event); err != nil {
			return err
		}

		result = SetUserStatusResult{User: updated}
		return nil
	})
	if err != nil {
		return SetUserStatusResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
