package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// ActionContentResolve is the policy action evaluated for acting on a
// binding in the review queue.
const ActionContentResolve policy.Action = "content.resolve"

// BindingResolution is the decision a reviewer makes about a pending binding.
type BindingResolution string

const (
	// ResolveConfirm settles a binding against its node — a merge.
	ResolveConfirm BindingResolution = "confirm"
	// ResolveReject declines the match, keeping the row so the same weak
	// match is not proposed again.
	ResolveReject BindingResolution = "reject"
)

// ResolveContentBindingCommand acts on a binding a person is reviewing
// (ADR 0013). The three operations the model describes all pass through here:
// a merge is Confirm, a rejection is Reject, and a split is Confirm with
// MoveToNodeID set — the binding moves to a different node and the source is
// never re-fingerprinted.
type ResolveContentBindingCommand struct {
	CallerSessionID domain.SessionID
	BindingID       domain.SourceBindingID
	Resolution      BindingResolution
	// MoveToNodeID re-targets the binding before confirming — a split. It is
	// only valid with Confirm: rejecting a match and moving it at once is
	// contradictory.
	MoveToNodeID domain.NodeID
}

// ResolveContentBindingResult carries the updated binding.
type ResolveContentBindingResult struct {
	Binding domain.SourceBinding
}

func validateResolveContentBindingCommand(cmd ResolveContentBindingCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.BindingID == "" {
		return contracts.NewError(contracts.InvalidArgument, "binding id is required")
	}
	switch cmd.Resolution {
	case ResolveConfirm:
	case ResolveReject:
		if cmd.MoveToNodeID != "" {
			return contracts.NewError(contracts.InvalidArgument, "a rejected binding cannot also be moved")
		}
	default:
		return contracts.NewError(contracts.InvalidArgument, "resolution must be confirm or reject")
	}
	return nil
}

// ResolveContentBinding settles one entry in the review queue.
func (s *Service) ResolveContentBinding(ctx context.Context, cmd ResolveContentBindingCommand) (ResolveContentBindingResult, error) {
	// 1. validate command shape.
	if err := validateResolveContentBindingCommand(cmd); err != nil {
		return ResolveContentBindingResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return ResolveContentBindingResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentResolve, policy.Resource{Type: "content", ID: string(cmd.BindingID)}, policy.PolicyContext{}); err != nil {
		return ResolveContentBindingResult{}, err
	}

	var result ResolveContentBindingResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5. load the binding under review.
		binding, err := tx.SourceBindings().FindByID(ctx, cmd.BindingID)
		if err != nil {
			return err
		}

		now := s.clock.Now()

		// 6. apply the resolution. A split moves first — the target must
		// exist — then confirms, keeping the source's identity untouched.
		switch cmd.Resolution {
		case ResolveReject:
			binding.Status = domain.BindingRejected
			binding.UpdatedAt = now
		case ResolveConfirm:
			if cmd.MoveToNodeID != "" {
				if _, err := tx.Nodes().FindByID(ctx, cmd.MoveToNodeID); err != nil {
					return err
				}
				binding = binding.MoveTo(cmd.MoveToNodeID, now)
			}
			binding = binding.Confirm(now)
		}

		// 7. persist state and the outbox event in the same transaction.
		updated, err := tx.SourceBindings().Update(ctx, binding)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("content.binding.resolved", []byte(string(updated.ID)), string(callerID)),
		}); err != nil {
			return err
		}

		result = ResolveContentBindingResult{Binding: updated}
		return nil
	})
	if err != nil {
		return ResolveContentBindingResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
