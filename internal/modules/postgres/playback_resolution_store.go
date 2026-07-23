// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// playbackResolutionStore is the PostgreSQL contracts.PlaybackResolutionStore:
// one row per (part, capability class), upserted on write (ADR 0049).
type playbackResolutionStore struct {
	q queryer
}

// NewPlaybackResolutionStore builds a pool-backed PlaybackResolutionStore.
//
// Pool-backed rather than transaction-scoped, unlike most stores here, because
// the cache is written *after* the stream has started — it must not be inside
// the unit of work of anything, and it has no outbox event to commit with.
func NewPlaybackResolutionStore(pool *pgxpool.Pool) contracts.PlaybackResolutionStore {
	return &playbackResolutionStore{q: pool}
}

func (s *playbackResolutionStore) Get(ctx context.Context, partID, capabilityClass string) (domain.PlaybackResolution, error) {
	row := s.q.QueryRow(ctx,
		`SELECT part_id, capability_class, url, headers, resolved_at
		   FROM playback_resolutions
		  WHERE part_id = $1 AND capability_class = $2`, partID, capabilityClass)

	var res domain.PlaybackResolution
	var headers []byte
	if err := row.Scan(&res.PartID, &res.CapabilityClass, &res.URL, &headers, &res.ResolvedAt); err != nil {
		if isNoRows(err) {
			return domain.PlaybackResolution{}, contracts.NewError(contracts.NotFound, "no cached resolution for this part and client")
		}
		return domain.PlaybackResolution{}, mapError("get playback resolution", err)
	}
	if len(headers) > 0 {
		// A header set that will not decode is a corrupt cache entry, not a
		// reason to fail a play: the URL beside it is still the useful part, and
		// the origin will re-resolve if fetching bare turns out not to work.
		_ = json.Unmarshal(headers, &res.Headers)
	}
	if len(res.Headers) == 0 {
		res.Headers = nil
	}
	return res, nil
}

func (s *playbackResolutionStore) Set(ctx context.Context, res domain.PlaybackResolution) error {
	headers := []byte("{}")
	if len(res.Headers) > 0 {
		encoded, err := json.Marshal(res.Headers)
		if err != nil {
			return contracts.WrapError(contracts.Internal, "encode playback resolution headers", err)
		}
		headers = encoded
	}
	_, err := s.q.Exec(ctx,
		`INSERT INTO playback_resolutions (part_id, capability_class, url, headers, resolved_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (part_id, capability_class) DO UPDATE
		   SET url = EXCLUDED.url,
		       headers = EXCLUDED.headers,
		       resolved_at = EXCLUDED.resolved_at`,
		res.PartID, res.CapabilityClass, res.URL, headers, res.ResolvedAt,
	)
	if err != nil {
		return mapError("set playback resolution", err)
	}
	return nil
}

func (s *playbackResolutionStore) Delete(ctx context.Context, partID, capabilityClass string) error {
	tag, err := s.q.Exec(ctx,
		`DELETE FROM playback_resolutions WHERE part_id = $1 AND capability_class = $2`,
		partID, capabilityClass)
	if err != nil {
		return mapError("delete playback resolution", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "no cached resolution for this part and client")
	}
	return nil
}
