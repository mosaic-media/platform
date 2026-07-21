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

func validateAddContentChildCommand(cmd v1.AddContentChildCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.ParentID == "" {
		return contracts.NewError(contracts.InvalidArgument, "parent id is required")
	}
	if cmd.Title == "" {
		return contracts.NewError(contracts.InvalidArgument, "title is required")
	}
	switch cmd.Kind {
	case v1.NodeContainer:
		if cmd.ContainerType == "" {
			return contracts.NewError(contracts.InvalidArgument, "container type is required for a container")
		}
		if cmd.ItemType != "" {
			return contracts.NewError(contracts.InvalidArgument, "item type must be empty for a container")
		}
	case v1.NodeItem:
		if cmd.ItemType == "" {
			return contracts.NewError(contracts.InvalidArgument, "item type is required for an item")
		}
		if cmd.ContainerType != "" {
			return contracts.NewError(contracts.InvalidArgument, "container type must be empty for an item")
		}
	case v1.NodeWork:
		return contracts.NewError(contracts.InvalidArgument, "a work cannot be a child")
	default:
		return contracts.NewError(contracts.InvalidArgument, "kind must be container or item")
	}
	return nil
}

// AddContentChild inserts one layer of the containment tree. A child inherits
// its work id and media type from its parent (ADR 0013), so a season cannot
// declare a different media type than its series.
func (s *Service) AddContentChild(ctx context.Context, cmd v1.AddContentChildCommand) (v1.AddContentChildResult, error) {
	// 1. validate command shape.
	if err := validateAddContentChildCommand(cmd); err != nil {
		return v1.AddContentChildResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return v1.AddContentChildResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentCreate, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return v1.AddContentChildResult{}, err
	}

	var result v1.AddContentChildResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5. load the parent — it fixes the tree and the media type, and its
		// absence is the caller's error (NotFound propagates).
		parent, err := tx.Nodes().FindByID(ctx, cmd.ParentID)
		if err != nil {
			return err
		}

		now := s.clock.Now()
		parentID := cmd.ParentID
		child := v1.Node{
			ID:            v1.NodeID(s.contentIDs.NewID()),
			WorkID:        parent.WorkID,
			ParentID:      &parentID,
			Kind:          cmd.Kind,
			MediaType:     parent.MediaType,
			ContainerType: cmd.ContainerType,
			ItemType:      cmd.ItemType,
			Title:         cmd.Title,
			NaturalOrder:  cmd.NaturalOrder,
			Status:        v1.NodeActive,
			ExternalIDs:   cmd.ExternalIDs,
			Attributes:    cmd.Attributes,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		// 7. persist state and the outbox event in the same transaction.
		created, err := tx.Nodes().Create(ctx, child)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("content.node.created", []byte(string(created.ID)), string(callerID)),
		}); err != nil {
			return err
		}

		result = v1.AddContentChildResult{Node: created}
		return nil
	})
	if err != nil {
		return v1.AddContentChildResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
