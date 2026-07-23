// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// PlaybackStateStore persists one viewer's position in one item (ADR 0046).
//
// It is the fifth content store and the first per-user one. Everything in the
// object graph so far is install-global — the same nodes, the same tree, the
// same candidate releases for everybody — and this is the first content state
// that differs between two users of one Mosaic.
//
// It joins Tx rather than sitting beside it like the resolution cache, because
// a position is state rather than a cache: it is the thing a user would be upset
// to lose, it emits an outbox event, and the two must commit together.
type PlaybackStateStore interface {
	// Get returns one user's state for one node, NotFound when they have never
	// started it. NotFound rather than a zero value because "never started" and
	// "started and at zero" are different answers and a caller renders them
	// differently.
	Get(ctx context.Context, userID domain.UserID, nodeID v1.NodeID) (v1.PlaybackState, error)
	// ListByNodes returns the states that exist among nodeIDs, keyed by node.
	// Nodes with no state are absent rather than present-and-zero — a season's
	// worth of watched marks in one query.
	ListByNodes(ctx context.Context, userID domain.UserID, nodeIDs []v1.NodeID) (map[v1.NodeID]v1.PlaybackState, error)
	// ListInProgress returns started-but-unfinished states, most recently
	// touched first, capped by limit.
	ListInProgress(ctx context.Context, userID domain.UserID, limit int) ([]v1.PlaybackState, error)
	// Upsert writes one state, replacing whatever was there.
	Upsert(ctx context.Context, userID domain.UserID, state v1.PlaybackState) (v1.PlaybackState, error)
}
