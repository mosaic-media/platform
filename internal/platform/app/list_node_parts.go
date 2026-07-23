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
// `ContentService` can attach a Part and has no read for one anywhere, so a
// capability cannot see what it wrote. The emit-side needs the read now — a
// detail screen cannot offer Play without knowing there is something to play —
// and the SDK addition is a separate, deliberate change rather than something
// to slip in behind an emit-side need.
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

	callerID, err := s.authenticateCaller(ctx, q.Caller)
	if err != nil {
		return ListNodePartsResult{}, err
	}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRead, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
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
// It is an entry point — the screens transport calls it directly (ADR 0036's
// affordance gate) — so it clears the boundary itself, once, and then reads
// stores directly rather than re-entering GetContentNode and ListNodeParts.
// Re-entering was the ADR 0066 defect in its most expensive form: a work whose
// playable item is its twentieth child cost twenty-one authenticate-plus-
// authorize cycles to discover one Part id.
//
// Collapsing them is decision-equivalent under today's policy engine, which
// ignores Resource entirely. The per-child calls also authorised bare
// "content" with no id, so the single check made here — against the work — is
// the more specific of the two. If relationship- or attribute-based rules ever
// make Resource load-bearing, authorising each child becomes a real decision to
// take deliberately, rather than one this loop was silently making.
//
// The two failure paths are deliberately different. A boundary failure is
// returned: an expired session must not look like a work with nothing to play,
// which is how the swallow here previously rendered it. A store read that fails
// still degrades to "nothing playable", so a transient blip omits the Play
// button rather than failing a detail screen whose metadata already arrived.
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
// thin alias over ListNodeParts: the Platform grew the read first, for the
// emit-side's Play affordance, and the SDK grew it when a module needed to see
// its own writes to refresh a candidate set.
func (s *Service) ListContentParts(ctx context.Context, q v1.ListContentPartsQuery) (v1.ListContentPartsResult, error) {
	res, err := s.ListNodeParts(ctx, ListNodePartsQuery{Caller: q.Caller, NodeID: q.NodeID})
	if err != nil {
		return v1.ListContentPartsResult{}, err
	}
	return v1.ListContentPartsResult{Parts: res.Parts}, nil
}
