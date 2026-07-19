package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// ActionContentResolve is the policy action evaluated for acting on a
// binding in the review queue.
const ActionContentResolve policy.Action = "content.resolve"

func validateResolveContentBindingCommand(cmd v1.ResolveContentBindingCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.BindingID == "" {
		return contracts.NewError(contracts.InvalidArgument, "binding id is required")
	}
	switch cmd.Resolution {
	case v1.ResolveConfirm:
	case v1.ResolveReject:
		if cmd.MoveToNodeID != "" {
			return contracts.NewError(contracts.InvalidArgument, "a rejected binding cannot also be moved")
		}
	default:
		return contracts.NewError(contracts.InvalidArgument, "resolution must be confirm or reject")
	}
	return nil
}

// ResolveContentBinding settles one entry in the review queue. A merge is
// Confirm, a decline is Reject, and a split is Confirm with MoveToNodeID —
// the binding moves and the source's identity is never re-resolved (ADR 0013).
func (s *Service) ResolveContentBinding(ctx context.Context, cmd v1.ResolveContentBindingCommand) (v1.ResolveContentBindingResult, error) {
	// 1. validate command shape.
	if err := validateResolveContentBindingCommand(cmd); err != nil {
		return v1.ResolveContentBindingResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return v1.ResolveContentBindingResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentResolve, policy.Resource{Type: "content", ID: string(cmd.BindingID)}, policy.PolicyContext{}); err != nil {
		return v1.ResolveContentBindingResult{}, err
	}

	var result v1.ResolveContentBindingResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5. load the binding under review.
		binding, err := tx.SourceBindings().FindByID(ctx, cmd.BindingID)
		if err != nil {
			return err
		}

		now := s.clock.Now()

		// 6. apply the resolution. State transitions are Platform operations,
		// so they are performed here rather than by a method on the published
		// model (ADR 0016). A split moves first — the target must exist — then
		// confirms, keeping the source's identity (method, confidence)
		// untouched.
		switch cmd.Resolution {
		case v1.ResolveReject:
			binding.Status = v1.BindingRejected
		case v1.ResolveConfirm:
			if cmd.MoveToNodeID != "" {
				if _, err := tx.Nodes().FindByID(ctx, cmd.MoveToNodeID); err != nil {
					return err
				}
				binding.NodeID = cmd.MoveToNodeID
			}
			binding.Status = v1.BindingConfirmed
		}
		binding.UpdatedAt = now

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

		result = v1.ResolveContentBindingResult{Binding: updated}
		return nil
	})
	if err != nil {
		return v1.ResolveContentBindingResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
