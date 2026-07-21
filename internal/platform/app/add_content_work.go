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

// ContentService is the published surface (contracts/platform/v1) these
// handlers implement. The assertion fails to compile if a method drifts from
// the contract a capability depends on (ADR 0016).
var _ v1.ContentService = (*Service)(nil)

// ActionContentCreate is the policy action evaluated for adding catalogue
// structure — works, containers, items and parts.
const ActionContentCreate policy.Action = "content.create"

func validateAddContentWorkCommand(cmd v1.AddContentWorkCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.MediaType == "" {
		return contracts.NewError(contracts.InvalidArgument, "media type is required")
	}
	if cmd.Title == "" {
		return contracts.NewError(contracts.InvalidArgument, "title is required")
	}
	return nil
}

// AddContentWork follows the command boundary: validate, authenticate,
// authorize, open a UnitOfWork, apply the domain shape of a Work, persist it
// with its outbox event in one transaction, and return the committed value.
func (s *Service) AddContentWork(ctx context.Context, cmd v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	// 1. validate command shape.
	if err := validateAddContentWorkCommand(cmd); err != nil {
		return v1.AddContentWorkResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return v1.AddContentWorkResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentCreate, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return v1.AddContentWorkResult{}, err
	}

	var result v1.AddContentWorkResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		now := s.clock.Now()
		id := v1.NodeID(s.contentIDs.NewID())

		// 5-6. apply the domain shape of a Work: it roots its own tree, so
		// work id is its own id and there is no parent.
		work := v1.Node{
			ID:          id,
			WorkID:      id,
			ParentID:    nil,
			Kind:        v1.NodeWork,
			MediaType:   cmd.MediaType,
			Title:       cmd.Title,
			Status:      v1.NodeActive,
			ExternalIDs: cmd.ExternalIDs,
			Attributes:  cmd.Attributes,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		// 7. persist state and the outbox event in the same transaction.
		created, err := tx.Nodes().Create(ctx, work)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("content.work.created", []byte(string(created.ID)), string(callerID)),
		}); err != nil {
			return err
		}

		result = v1.AddContentWorkResult{Work: created}
		return nil
	})
	if err != nil {
		return v1.AddContentWorkResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
