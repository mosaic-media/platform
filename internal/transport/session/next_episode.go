// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// upNext is the episode to offer after the one now playing (web#4).
type upNext struct {
	label, partID, nodeID, title string
}

// nextEpisodeUp finds the episode that follows the one playing and resolves it
// to a play input, or returns nil when there is nothing to offer: the current
// node is not an episode, it is the last one, or the next has no playable bytes.
//
// "Next" is the containment tree, not a relation — platform#9 keeps series →
// season → episode as the node tree, and the association graph carries no
// episode order. So this walks the tree: the next sibling in the season, else
// the first episode of a later season. Every read that fails yields no next-up
// rather than an error, because a missing "Next episode" control is a smaller
// fault than a play delayed or failed for want of one. It runs after the ticket
// is minted, so a slow or failing walk costs the control, never the play.
func (h *Handler) nextEpisodeUp(ctx context.Context, caller v1.Caller, currentID v1.NodeID) *upNext {
	if currentID == "" {
		return nil
	}
	cur, err := h.svc.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: currentID})
	if err != nil || cur.Node.ItemType != v1.ItemEpisode || cur.Node.ParentID == nil {
		return nil
	}
	next, ok := h.followingEpisode(ctx, caller, cur.Node)
	if !ok {
		return nil
	}
	part, playable, err := h.svc.FirstPlayablePart(ctx, caller, next.ID)
	if err != nil || !playable {
		return nil
	}
	// The player chrome carries the series title, as the current play does; the
	// episode names the control itself.
	title := next.Title
	if work, err := h.svc.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: next.WorkID}); err == nil {
		title = work.Node.Title
	}
	return &upNext{label: next.Title, partID: string(part.ID), nodeID: string(next.ID), title: title}
}

// followingEpisode returns the episode after cur in playback order: the next
// item in the same season, or failing that the first episode of the earliest
// later season.
func (h *Handler) followingEpisode(ctx context.Context, caller v1.Caller, cur v1.Node) (v1.Node, bool) {
	season, err := h.svc.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: *cur.ParentID, WithChildren: true})
	if err != nil {
		return v1.Node{}, false
	}
	if ep, ok := episodeAfter(season.Children, cur.ID); ok {
		return ep, true
	}
	if season.Node.ParentID == nil {
		return v1.Node{}, false
	}
	series, err := h.svc.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: *season.Node.ParentID, WithChildren: true})
	if err != nil {
		return v1.Node{}, false
	}
	for _, s := range seasonsAfter(series.Children, *cur.ParentID) {
		eps, err := h.svc.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: s.ID, WithChildren: true})
		if err != nil {
			continue
		}
		for _, e := range eps.Children {
			if e.ItemType == v1.ItemEpisode {
				return e, true
			}
		}
	}
	return v1.Node{}, false
}

// episodeAfter returns the first episode item positioned after id in an ordered
// child list, so a run of specials or a non-episode child between two episodes
// is stepped over rather than offered.
func episodeAfter(children []v1.Node, id v1.NodeID) (v1.Node, bool) {
	for i, c := range children {
		if c.ID != id {
			continue
		}
		for _, n := range children[i+1:] {
			if n.ItemType == v1.ItemEpisode {
				return n, true
			}
		}
		return v1.Node{}, false
	}
	return v1.Node{}, false
}

// seasonsAfter returns the child containers ordered after the one with id.
func seasonsAfter(children []v1.Node, id v1.NodeID) []v1.Node {
	for i, c := range children {
		if c.ID == id {
			return children[i+1:]
		}
	}
	return nil
}
