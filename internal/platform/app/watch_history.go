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

// Watch history — the per-user pass over a shared library (platform#59).
//
// It is a Platform query and deliberately not on the SDK's ContentService. The
// playback methods that are published are there because a consumer module
// records progress as its invoking user; nothing about a module's job requires
// reading a person's viewing history back, and putting it on the module surface
// would hand every installed extension the one list platform#59 keeps private.

// ListWatchHistoryQuery reads what the caller has watched, most recently
// touched first.
//
// There is no user parameter and there must not be one. A household shares one
// library and shares nothing about how each person uses it; a history that could
// be asked about somebody else turns the least shareable thing on the screen
// into a feed.
type ListWatchHistoryQuery struct {
	Caller v1.Caller
	// Limit caps the result, 0 for the store's own default.
	Limit int
}

// WatchedItem pairs a viewer's state with the item it belongs to.
//
// The node travels with the state because every caller needs both, and fetching
// them separately is a query per row — the same reason the continue-watching
// read carries one.
type WatchedItem struct {
	Node  v1.Node
	State v1.PlaybackState
}

// ListWatchHistoryResult carries the history in order.
type ListWatchHistoryResult struct {
	Items []WatchedItem
}

// ListWatchHistory reads the caller's own viewing history.
//
// It authorises playback.read — the same action the continue-watching rail and a
// resume offset use, and separate from content.read for the reason stated where
// it is defined: seeing the library is not the same as seeing what somebody
// watched. An ordinary household account holds it, because it is reading its
// own.
func (s *Service) ListWatchHistory(ctx context.Context, q ListWatchHistoryQuery) (ListWatchHistoryResult, error) {
	// 1. validate query shape.
	if q.Caller.Session == "" {
		return ListWatchHistoryResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	// 2-3. authenticate the caller and authorize the action.
	az, err := s.enter(ctx, q.Caller, ActionPlaybackRead, policy.Resource{Type: "playback"})
	if err != nil {
		return ListWatchHistoryResult{}, err
	}
	if s.playbackStates == nil || s.nodes == nil {
		return ListWatchHistoryResult{}, errNoPlaybackStore
	}

	states, err := s.playbackStates.ListWatched(ctx, az.userID, q.Limit)
	if err != nil {
		return ListWatchHistoryResult{}, err
	}

	items := make([]WatchedItem, 0, len(states))
	for _, state := range states {
		node, err := s.nodes.FindByID(ctx, state.NodeID)
		if err != nil {
			// A history entry whose node has gone is a row the cascade should
			// have removed. Skipping it keeps the screen rendering rather than
			// failing all of it for one stale row, the same choice the
			// continue-watching read makes.
			if contracts.CategoryOf(err) == contracts.NotFound {
				continue
			}
			return ListWatchHistoryResult{}, err
		}
		items = append(items, WatchedItem{Node: node, State: state})
	}
	return ListWatchHistoryResult{Items: items}, nil
}
