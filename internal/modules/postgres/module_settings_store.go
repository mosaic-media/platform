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

// moduleSettingsStore is the PostgreSQL contracts.ModuleSettingsStore: one
// jsonb document per module id, upserted on write.
type moduleSettingsStore struct {
	q queryer
}

// NewModuleSettingsStore builds a pool-backed ModuleSettingsStore for reads.
func NewModuleSettingsStore(pool *pgxpool.Pool) contracts.ModuleSettingsStore {
	return &moduleSettingsStore{q: pool}
}

// emptyDocument is the settings a module with no row yet reads back, so first
// use is an empty object rather than NotFound.
var emptyDocument = []byte("{}")

func (s *moduleSettingsStore) Get(ctx context.Context, moduleID string) (domain.ModuleSettings, error) {
	row := s.q.QueryRow(ctx,
		`SELECT module_id, settings, updated_at FROM module_settings WHERE module_id = $1`, moduleID)
	var ms domain.ModuleSettings
	if err := row.Scan(&ms.ModuleID, &ms.Settings, &ms.UpdatedAt); err != nil {
		if isNoRows(err) {
			return domain.ModuleSettings{ModuleID: moduleID, Settings: emptyDocument}, nil
		}
		return domain.ModuleSettings{}, mapError("get module settings", err)
	}
	return ms, nil
}

func (s *moduleSettingsStore) Set(ctx context.Context, ms domain.ModuleSettings) (domain.ModuleSettings, error) {
	if len(ms.Settings) == 0 {
		ms.Settings = emptyDocument
	}
	_, err := s.q.Exec(ctx,
		`INSERT INTO module_settings (module_id, settings, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (module_id) DO UPDATE
		   SET settings = EXCLUDED.settings, updated_at = EXCLUDED.updated_at`,
		ms.ModuleID, ms.Settings, ms.UpdatedAt,
	)
	if err != nil {
		return domain.ModuleSettings{}, mapError("set module settings", err)
	}
	return ms, nil
}
