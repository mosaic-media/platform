// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// watchAvailabilityStore is the PostgreSQL
// contracts.WatchAvailabilityStore: the queryable projection of what a metadata
// provider said about where a work can be watched.
type watchAvailabilityStore struct {
	q queryer
}

// NewWatchAvailabilityStore builds a pool-backed store. Both its callers are
// outside a transaction: the enrichment pass writes it beside the metadata
// document it already writes that way, and the refresh reads it to decide what
// to ask about next.
func NewWatchAvailabilityStore(pool *pgxpool.Pool) contracts.WatchAvailabilityStore {
	return &watchAvailabilityStore{q: pool}
}

func (s *watchAvailabilityStore) Upsert(ctx context.Context, a contracts.WatchAvailability) (contracts.WatchAvailability, error) {
	providers := a.Providers
	if providers == nil {
		// The column is NOT NULL, and "the provider was asked and named nothing"
		// is a real answer worth storing: it is what makes a title that left
		// every service stop matching a facet, which is the whole point of the
		// refresh.
		providers = []string{}
	}
	_, err := s.q.Exec(ctx,
		`INSERT INTO node_watch_availability (node_id, region, providers, checked_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (node_id) DO UPDATE
		   SET region = EXCLUDED.region, providers = EXCLUDED.providers,
		       checked_at = EXCLUDED.checked_at`,
		string(a.NodeID), a.Region, providers, a.CheckedAt,
	)
	if err != nil {
		return contracts.WatchAvailability{}, mapError("upsert watch availability", err)
	}
	a.Providers = providers
	return a, nil
}

func (s *watchAvailabilityStore) ListStale(ctx context.Context, limit int) ([]v1.NodeID, error) {
	if limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}
	rows, err := s.q.Query(ctx,
		// node_id breaks the tie, so the order is total. Without it a run and
		// the run after it could take the same rows and never reach the rest —
		// a refresh that starves half the library while reporting a full budget
		// every time.
		`SELECT node_id FROM node_watch_availability
		 ORDER BY checked_at, node_id
		 LIMIT $1`, limit)
	if err != nil {
		return nil, mapError("list stale watch availability", err)
	}
	defer rows.Close()

	var out []v1.NodeID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, mapError("list stale watch availability", err)
		}
		out = append(out, v1.NodeID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list stale watch availability", err)
	}
	return out, nil
}
