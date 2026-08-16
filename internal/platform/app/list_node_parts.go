// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ListNodePartsQuery reads the playable parts of an item node.
//
// This is the Platform-side half of a gap the published surface still has:
// ContentService can attach a Part and has no read for one, so a capability
// cannot see what it wrote. The emit-side needs the read — a detail screen
// cannot offer Play without knowing there is something to play — and the SDK
// addition is a separate, deliberate change.
type ListNodePartsQuery struct {
	Caller v1.Caller
	NodeID v1.NodeID
}

// ListNodePartsResult carries the node's parts in natural order, so the
// segments of a multi-disc edition come back in sequence.
type ListNodePartsResult struct {
	Parts []v1.Part
}

// ListNodeParts reads one item's parts. It opens no transaction and writes
// nothing.
func (s *Service) ListNodeParts(ctx context.Context, q ListNodePartsQuery) (ListNodePartsResult, error) {
	if q.Caller.Session == "" {
		return ListNodePartsResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.NodeID == "" {
		return ListNodePartsResult{}, contracts.NewError(contracts.InvalidArgument, "node id is required")
	}

	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return ListNodePartsResult{}, err
	}
	if s.parts == nil {
		return ListNodePartsResult{}, contracts.NewError(contracts.Unavailable, "no part store configured")
	}

	parts, err := s.parts.ListByNode(ctx, q.NodeID)
	if err != nil {
		return ListNodePartsResult{}, err
	}
	return ListNodePartsResult{Parts: parts}, nil
}

// FirstPlayablePart finds a part to play under a work, walking one level down to
// the first item that has one.
//
// It exists because a Work has no bytes: a film's part hangs off its feature
// item, a series' off each episode. Walking one level is enough for a movie and
// deliberately not enough for a series, where which episode plays is the user's
// choice rather than a default this should invent — so a series returns nothing
// and the detail screen offers Play per episode instead.
//
// It is an entry point — the screens transport calls it directly (platform#24's
// affordance gate) — so it clears the boundary itself, once, and then reads
// stores directly rather than re-entering GetContentNode and ListNodeParts.
// Re-entering costs one authenticate-plus-authorize cycle per child to discover
// one Part id (platform#41).
//
// Collapsing them is decision-equivalent under today's policy engine, which
// ignores Resource entirely, and the single check here is against the work where
// the per-child calls authorised bare "content" with no id. If relationship- or
// attribute-based rules ever make Resource load-bearing, authorising each child
// becomes a decision to take deliberately.
//
// The two failure paths are deliberately different. A boundary failure is
// returned, so an expired session does not look like a work with nothing to
// play. A failed store read degrades to "nothing playable", so a transient blip
// omits the Play button rather than failing a detail screen whose metadata has
// already arrived.
func (s *Service) FirstPlayablePart(ctx context.Context, caller v1.Caller, workID v1.NodeID) (v1.Part, bool, error) {
	if caller.Session == "" {
		return v1.Part{}, false, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if workID == "" {
		return v1.Part{}, false, contracts.NewError(contracts.InvalidArgument, "work id is required")
	}

	if _, err := s.enter(ctx, caller, ActionContentRead,
		policy.Resource{Type: "content", ID: string(workID)}); err != nil {
		return v1.Part{}, false, err
	}
	if s.parts == nil {
		return v1.Part{}, false, contracts.NewError(contracts.Unavailable, "no part store configured")
	}

	// ListChildren rather than a node read: the work itself was never used,
	// only its children, so fetching it was a query spent on nothing.
	children, err := s.nodes.ListChildren(ctx, workID)
	if err != nil {
		return v1.Part{}, false, nil
	}
	for _, child := range children {
		if child.Kind != v1.NodeItem {
			continue
		}
		parts, err := s.parts.ListByNode(ctx, child.ID)
		if err != nil || len(parts) == 0 {
			continue
		}
		return parts[0], true, nil
	}
	return v1.Part{}, false, nil
}

// ListContentParts satisfies the published ContentService (SDK v0.10.0). It is a
// thin alias over ListNodeParts, which a module uses to see its own writes when
// refreshing a candidate set.
func (s *Service) ListContentParts(ctx context.Context, q v1.ListContentPartsQuery) (v1.ListContentPartsResult, error) {
	res, err := s.ListNodeParts(ctx, ListNodePartsQuery{Caller: q.Caller, NodeID: q.NodeID})
	if err != nil {
		return v1.ListContentPartsResult{}, err
	}
	return v1.ListContentPartsResult{Parts: res.Parts}, nil
}
