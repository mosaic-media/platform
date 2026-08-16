// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
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
// the binding moves and the source's identity is never re-resolved (platform#9).
func (s *Service) ResolveContentBinding(ctx context.Context, cmd v1.ResolveContentBindingCommand) (v1.ResolveContentBindingResult, error) {
	if err := validateResolveContentBindingCommand(cmd); err != nil {
		return v1.ResolveContentBindingResult{}, err
	}

	az, err := s.enter(ctx, cmd.Caller, ActionContentResolve,
		policy.Resource{Type: "content", ID: string(cmd.BindingID)})
	if err != nil {
		return v1.ResolveContentBindingResult{}, err
	}

	var result v1.ResolveContentBindingResult

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		binding, err := tx.SourceBindings().FindByID(ctx, cmd.BindingID)
		if err != nil {
			return err
		}

		now := s.clock.Now()

		// State transitions are Platform operations, so they are performed here
		// rather than by a method on the published model (platform#12). A split
		// moves first — the target must exist — then confirms, keeping the
		// source's identity (method, confidence) untouched.
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

		updated, err := tx.SourceBindings().Update(ctx, binding)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "content.binding.resolved", []byte(string(updated.ID)), string(az.userID)),
		}); err != nil {
			return err
		}

		result = v1.ResolveContentBindingResult{Binding: updated}
		return nil
	})
	if err != nil {
		return v1.ResolveContentBindingResult{}, err
	}

	return result, nil
}
