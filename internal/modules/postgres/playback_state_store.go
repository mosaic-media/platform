// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// playbackStateStore is the PostgreSQL contracts.PlaybackStateStore: one row
// per (user, node), upserted as a viewer moves through an item (ADR 0046).
type playbackStateStore struct {
	q queryer
}

// NewPlaybackStateStore builds a pool-backed store for the read path — a detail
// screen asking where a viewer got to, and the continue-watching rail. Writes go
// through the UnitOfWork like every other mutation.
func NewPlaybackStateStore(pool *pgxpool.Pool) contracts.PlaybackStateStore {
	return &playbackStateStore{q: pool}
}

const playbackStateColumns = `node_id, part_id, position_ms, duration_ms, finished, finished_explicit, updated_at`

// defaultInProgressLimit caps a continue-watching read that named no limit. A
// rail is a rail: nobody scrolls to the fortieth thing they half-started.
const defaultInProgressLimit = 20

func (s *playbackStateStore) Get(ctx context.Context, userID domain.UserID, nodeID v1.NodeID) (v1.PlaybackState, error) {
	row := s.q.QueryRow(ctx,
		`SELECT `+playbackStateColumns+`
		   FROM playback_states WHERE user_id = $1 AND node_id = $2`,
		string(userID), string(nodeID))

	state, err := scanPlaybackState(row)
	if err != nil {
		if isNoRows(err) {
			return v1.PlaybackState{}, contracts.NewError(contracts.NotFound, "no playback state for this item")
		}
		return v1.PlaybackState{}, mapError("get playback state", err)
	}
	return state, nil
}

func (s *playbackStateStore) ListByNodes(ctx context.Context, userID domain.UserID, nodeIDs []v1.NodeID) (map[v1.NodeID]v1.PlaybackState, error) {
	if len(nodeIDs) == 0 {
		return map[v1.NodeID]v1.PlaybackState{}, nil
	}
	ids := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		ids[i] = string(id)
	}

	rows, err := s.q.Query(ctx,
		`SELECT `+playbackStateColumns+`
		   FROM playback_states WHERE user_id = $1 AND node_id = ANY($2)`,
		string(userID), ids)
	if err != nil {
		return nil, mapError("list playback states", err)
	}
	defer rows.Close()

	out := make(map[v1.NodeID]v1.PlaybackState, len(nodeIDs))
	for rows.Next() {
		state, err := scanPlaybackState(rows)
		if err != nil {
			return nil, mapError("list playback states", err)
		}
		out[state.NodeID] = state
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list playback states", err)
	}
	return out, nil
}

func (s *playbackStateStore) ListInProgress(ctx context.Context, userID domain.UserID, limit int) ([]v1.PlaybackState, error) {
	if limit <= 0 {
		limit = defaultInProgressLimit
	}
	// `NOT finished` matches the partial index exactly, and `position_ms > 0`
	// keeps out an item someone opened and closed without watching — which
	// otherwise appears at the top of the rail, since it is the most recently
	// touched thing there is.
	rows, err := s.q.Query(ctx,
		`SELECT `+playbackStateColumns+`
		   FROM playback_states
		  WHERE user_id = $1 AND NOT finished AND position_ms > 0
		  ORDER BY updated_at DESC
		  LIMIT $2`,
		string(userID), limit)
	if err != nil {
		return nil, mapError("list in-progress playback", err)
	}
	defer rows.Close()

	var out []v1.PlaybackState
	for rows.Next() {
		state, err := scanPlaybackState(rows)
		if err != nil {
			return nil, mapError("list in-progress playback", err)
		}
		out = append(out, state)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list in-progress playback", err)
	}
	return out, nil
}

func (s *playbackStateStore) Upsert(ctx context.Context, userID domain.UserID, state v1.PlaybackState) (v1.PlaybackState, error) {
	// A part id is optional: the position outlives the release that produced it,
	// which is the point of keying on the node.
	var partID *string
	if state.PartID != "" {
		id := string(state.PartID)
		partID = &id
	}

	_, err := s.q.Exec(ctx,
		`INSERT INTO playback_states
		     (user_id, node_id, part_id, position_ms, duration_ms, finished, finished_explicit, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (user_id, node_id) DO UPDATE
		   SET part_id           = EXCLUDED.part_id,
		       position_ms       = EXCLUDED.position_ms,
		       duration_ms       = EXCLUDED.duration_ms,
		       finished          = EXCLUDED.finished,
		       finished_explicit = EXCLUDED.finished_explicit,
		       updated_at        = EXCLUDED.updated_at`,
		string(userID), string(state.NodeID), partID,
		state.Position.Milliseconds(), state.Duration.Milliseconds(),
		state.Finished, state.FinishedExplicit, state.UpdatedAt,
	)
	if err != nil {
		return v1.PlaybackState{}, mapError("upsert playback state", err)
	}
	return state, nil
}

// scanPlaybackState reads one row. It takes the pgx Row interface so the
// single-row and multi-row paths share it.
func scanPlaybackState(row pgx.Row) (v1.PlaybackState, error) {
	var (
		state      v1.PlaybackState
		nodeID     string
		partID     *string
		positionMS int64
		durationMS int64
	)
	if err := row.Scan(&nodeID, &partID, &positionMS, &durationMS,
		&state.Finished, &state.FinishedExplicit, &state.UpdatedAt); err != nil {
		return v1.PlaybackState{}, err
	}
	state.NodeID = v1.NodeID(nodeID)
	if partID != nil {
		state.PartID = v1.PartID(*partID)
	}
	state.Position = time.Duration(positionMS) * time.Millisecond
	state.Duration = time.Duration(durationMS) * time.Millisecond
	return state, nil
}
