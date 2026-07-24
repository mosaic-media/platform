// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// installedExtensionStore is the PostgreSQL contracts.InstalledExtensionStore:
// one row per installed extension module, keyed by module id (ADR 0081).
type installedExtensionStore struct {
	q queryer
}

// NewInstalledExtensionStore builds a pool-backed store for reads — the boot
// re-adoption path reads the whole set outside a transaction.
func NewInstalledExtensionStore(pool *pgxpool.Pool) contracts.InstalledExtensionStore {
	return &installedExtensionStore{q: pool}
}

func (s *installedExtensionStore) List(ctx context.Context) ([]domain.InstalledExtension, error) {
	rows, err := s.q.Query(ctx,
		`SELECT module_id, repository, version, signed_by, installed_at
		 FROM installed_extensions ORDER BY module_id`)
	if err != nil {
		return nil, mapError("list installed extensions", err)
	}
	defer rows.Close()

	var out []domain.InstalledExtension
	for rows.Next() {
		var e domain.InstalledExtension
		if err := rows.Scan(&e.ModuleID, &e.Repository, &e.Version, &e.SignedBy, &e.InstalledAt); err != nil {
			return nil, mapError("scan installed extension", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate installed extensions", err)
	}
	return out, nil
}

func (s *installedExtensionStore) Upsert(ctx context.Context, e domain.InstalledExtension) (domain.InstalledExtension, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO installed_extensions (module_id, repository, version, signed_by, installed_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (module_id) DO UPDATE
		   SET repository = EXCLUDED.repository, version = EXCLUDED.version,
		       signed_by = EXCLUDED.signed_by, installed_at = EXCLUDED.installed_at`,
		e.ModuleID, e.Repository, e.Version, e.SignedBy, e.InstalledAt,
	)
	if err != nil {
		return domain.InstalledExtension{}, mapError("upsert installed extension", err)
	}
	return e, nil
}

func (s *installedExtensionStore) Remove(ctx context.Context, moduleID string) error {
	// DELETE of a missing row affects zero rows and is not an error, which is the
	// idempotent uninstall the contract promises.
	if _, err := s.q.Exec(ctx, `DELETE FROM installed_extensions WHERE module_id = $1`, moduleID); err != nil {
		return mapError("remove installed extension", err)
	}
	return nil
}
