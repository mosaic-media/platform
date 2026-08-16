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

// Setting a node's artwork (platform#47).
//
// This is the first content command that updates a node rather than creating
// one, and the absence of it is something platform#45 wrote down as owed: "there is
// no command that updates a stored work's fields, so a re-import does not
// refresh its artwork today."
//
// Two things need it now. The artwork enrichment pass resolves art for a work a
// module has already written (sdk#6), so it has a node and nowhere to put the
// result. And a user choosing a different poster is by definition an update —
// the feature storing artwork on the node was the precondition for.

func validateSetContentArtworkCommand(cmd v1.SetContentArtworkCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.NodeID == "" {
		return contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	// A candidate with no slot cannot be selected on and a candidate with no URL
	// cannot be rendered; either one is a caller bug that would otherwise sit in
	// the document unnoticed until a picker tried to draw it.
	for _, candidate := range cmd.Artwork.Candidates {
		if candidate.Slot == "" {
			return contracts.NewError(contracts.InvalidArgument, "an artwork candidate requires a slot")
		}
		if candidate.URL == "" {
			return contracts.NewError(contracts.InvalidArgument, "an artwork candidate requires a url")
		}
	}
	return nil
}

// SetContentArtwork replaces a node's stored artwork.
//
// It replaces rather than merges, deliberately: deciding what happens when two
// sources offer the same slot belongs to whoever assembled the set, not to a
// command that cannot see why it was called. A caller adding to what is there
// reads, merges and writes back.
func (s *Service) SetContentArtwork(ctx context.Context, cmd v1.SetContentArtworkCommand) (v1.SetContentArtworkResult, error) {
	if err := validateSetContentArtworkCommand(cmd); err != nil {
		return v1.SetContentArtworkResult{}, err
	}

	// content.create rather than a new action: this is curating the library, the
	// same authority that adds the node in the first place, and an ordinary
	// household account deliberately does not hold it (see roles.go). When a user
	// gets to pick their own poster that judgement is worth revisiting — it would
	// be the first content write an ordinary account should be able to make.
	az, err := s.enter(ctx, cmd.Caller, ActionContentCreate, policy.Resource{Type: "content"})
	if err != nil {
		return v1.SetContentArtworkResult{}, err
	}

	var result v1.SetContentArtworkResult

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		var err error
		result, err = s.setNodeArtworkWithin(ctx, tx, az, cmd)
		return err
	})
	if err != nil {
		return v1.SetContentArtworkResult{}, err
	}

	return result, nil
}

// setNodeArtworkWithin replaces the node's artwork and appends the event that
// says so, in one transaction.
//
// Any kind of node may carry artwork — a work has key art, a season container
// its own poster, an item a still — so there is no kind check here, unlike
// attaching a Part.
func (s *Service) setNodeArtworkWithin(ctx context.Context, tx contracts.Tx, az authorized, cmd v1.SetContentArtworkCommand) (v1.SetContentArtworkResult, error) {
	node, err := tx.Nodes().FindByID(ctx, cmd.NodeID)
	if err != nil {
		return v1.SetContentArtworkResult{}, err
	}

	node.Artwork = cmd.Artwork
	node.UpdatedAt = s.clock.Now()

	updated, err := tx.Nodes().Update(ctx, node)
	if err != nil {
		return v1.SetContentArtworkResult{}, err
	}
	if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
		Event: s.newEvent(ctx, "content.artwork.set", []byte(string(updated.ID)), string(az.userID)),
	}); err != nil {
		return v1.SetContentArtworkResult{}, err
	}

	return v1.SetContentArtworkResult{Node: updated}, nil
}
