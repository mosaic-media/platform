package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// ActionUserStatusUpdate is the policy action evaluated for SetUserStatus.
const ActionUserStatusUpdate policy.Action = "user.status.update"

// SetUserStatusCommand sets a target user's admin-managed status
// (MEG-015 §09 — Users: "admin-managed status"). CallerSessionID must
// belong to an authorized administrator, not the target user (MEG-009 §04
// — Administrative Operations), matching CreateLocalUser's shape.
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

// SetUserStatus implements the command boundary from MEG-015 §04.
func (s *Service) SetUserStatus(ctx context.Context, cmd SetUserStatusCommand) (SetUserStatusResult, error) {
	// 1. validate command shape.
	if err := validateSetUserStatusCommand(cmd); err != nil {
		return SetUserStatusResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return SetUserStatusResult{}, err
	}

	// 3. authorize action through policy.
	resource := policy.Resource{Type: "user", ID: string(cmd.TargetUserID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionUserStatusUpdate, resource, policy.PolicyContext{}); err != nil {
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
		event := domain.OutboxEvent{Event: s.newEvent("user.status_changed", []byte(string(cmd.Status)), string(callerID))}
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
