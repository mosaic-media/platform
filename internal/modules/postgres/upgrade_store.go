// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// upgradeStore is the pending-request half of platform#77.
type upgradeStore struct{ q queryer }

// NewUpgradeStore builds the store over a pool.
func NewUpgradeStore(pool *pgxpool.Pool) contracts.UpgradeStore { return &upgradeStore{q: pool} }

// Request replaces whatever was pending.
//
// **Settling the old one first is what makes the unique index hold**, and
// replacing rather than refusing is deliberate: the second press is the one
// that reflects what the person currently wants, and refusing it would leave an
// install waiting on a version somebody changed their mind about.
func (s *upgradeStore) Request(ctx context.Context, request domain.UpgradeRequest) error {
	if _, err := s.q.Exec(ctx,
		`UPDATE upgrade_requests SET settled_at = now() WHERE settled_at IS NULL`); err != nil {
		return mapError("upgrade request", err)
	}
	_, err := s.q.Exec(ctx,
		`INSERT INTO upgrade_requests (id, version, requested_at, requested_by)
		 VALUES ($1, $2, $3, $4)`,
		request.ID, request.Version, request.RequestedAt, request.RequestedBy)
	return mapError("upgrade request", err)
}

// Pending returns the request waiting, or NotFound when nothing is.
func (s *upgradeStore) Pending(ctx context.Context) (domain.UpgradeRequest, error) {
	row := s.q.QueryRow(ctx,
		`SELECT id, version, requested_by, requested_at
		   FROM upgrade_requests WHERE settled_at IS NULL
		  ORDER BY requested_at DESC LIMIT 1`)

	var request domain.UpgradeRequest
	err := row.Scan(&request.ID, &request.Version, &request.RequestedBy, &request.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UpgradeRequest{}, contracts.NewError(contracts.NotFound, "no upgrade has been requested")
	}
	if err != nil {
		return domain.UpgradeRequest{}, mapError("upgrade request", err)
	}
	return request, nil
}

// Settle closes the pending request. Closing nothing is success: settlement is
// driven by observing the running version, and observing it twice must not be
// an error.
func (s *upgradeStore) Settle(ctx context.Context) error {
	_, err := s.q.Exec(ctx, `UPDATE upgrade_requests SET settled_at = now() WHERE settled_at IS NULL`)
	return mapError("upgrade request", err)
}
