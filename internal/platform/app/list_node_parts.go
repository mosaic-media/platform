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
func (s *Service) FirstPlayablePart(ctx context.Context, caller v1.Caller, workID v1.NodeID) (v1.Part, bool) {
	node, err := s.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: workID, WithChildren: true})
	if err != nil {
		return v1.Part{}, false
	}
	for _, child := range node.Children {
		if child.Kind != v1.NodeItem {
			continue
		}
		res, err := s.ListNodeParts(ctx, ListNodePartsQuery{Caller: caller, NodeID: child.ID})
		if err != nil || len(res.Parts) == 0 {
			continue
		}
		return res.Parts[0], true
	}
	return v1.Part{}, false
}
