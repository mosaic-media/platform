package app

import (
	"context"

	v1 "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// AddContentChildCommand adds a container or item beneath an existing node —
// a season under a series, an episode under a season, a chapter under a
// volume (ADR 0013).
//
// Two fields a caller might expect to set are derived from the parent
// instead. A child belongs to the same tree as its parent, so its work id is
// the parent's work id, not the caller's to choose. And media type is carried
// on every node so a node is interpretable without walking to its root; a
// child therefore inherits its parent's, which also stops a season from
// declaring a different media type than its series.
type AddContentChildCommand struct {
	CallerSessionID domain.SessionID
	ParentID        v1.NodeID
	// Kind must be container or item. A work is never a child, so it is not
	// accepted here.
	Kind v1.NodeKind
	// ContainerType applies when Kind is container, ItemType when it is item;
	// the other must be empty. Both are open vocabularies, canonicalised on
	// write (ADR 0015).
	ContainerType v1.ContainerType
	ItemType      v1.ItemType
	Title         string
	// NaturalOrder places the child among its siblings. A float so an
	// insertion does not renumber the rest (ADR 0013).
	NaturalOrder float64
	ExternalIDs  []byte
	Attributes   []byte
}

// AddContentChildResult carries the committed child.
type AddContentChildResult struct {
	Node v1.Node
}

func validateAddContentChildCommand(cmd AddContentChildCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
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

// AddContentChild inserts one layer of the containment tree.
func (s *Service) AddContentChild(ctx context.Context, cmd AddContentChildCommand) (AddContentChildResult, error) {
	// 1. validate command shape.
	if err := validateAddContentChildCommand(cmd); err != nil {
		return AddContentChildResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return AddContentChildResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentCreate, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return AddContentChildResult{}, err
	}

	var result AddContentChildResult

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

		result = AddContentChildResult{Node: created}
		return nil
	})
	if err != nil {
		return AddContentChildResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
