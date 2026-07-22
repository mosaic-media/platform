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

// userPreferenceStore is the PostgreSQL contracts.UserPreferenceStore: one row
// per (user, key), upserted on write.
type userPreferenceStore struct {
	q queryer
}

// NewUserPreferenceStore builds a pool-backed store for reads. Writes go
// through the UnitOfWork, which binds the same type to a transaction.
func NewUserPreferenceStore(pool *pgxpool.Pool) contracts.UserPreferenceStore {
	return &userPreferenceStore{q: pool}
}

// nullValue is what an unset JSON value stores as, matching the column default.
var nullValue = []byte("null")

func (s *userPreferenceStore) Get(ctx context.Context, userID domain.UserID, key string) (domain.UserPreference, error) {
	row := s.q.QueryRow(ctx,
		`SELECT user_id, key, value, updated_at FROM user_preferences
		  WHERE user_id = $1 AND key = $2`, string(userID), key)

	var pref domain.UserPreference
	if err := row.Scan(&pref.UserID, &pref.Key, &pref.Value, &pref.UpdatedAt); err != nil {
		if isNoRows(err) {
			// NotFound rather than a zero value: "never set" and "set to
			// false" are different answers, and a caller with its own default
			// needs to tell them apart.
			return domain.UserPreference{}, contracts.NewError(contracts.NotFound, "preference not set")
		}
		return domain.UserPreference{}, mapError("get user preference", err)
	}
	return pref, nil
}

func (s *userPreferenceStore) List(ctx context.Context, userID domain.UserID) ([]domain.UserPreference, error) {
	rows, err := s.q.Query(ctx,
		`SELECT user_id, key, value, updated_at FROM user_preferences
		  WHERE user_id = $1 ORDER BY key`, string(userID))
	if err != nil {
		return nil, mapError("list user preferences", err)
	}
	defer rows.Close()

	var prefs []domain.UserPreference
	for rows.Next() {
		var pref domain.UserPreference
		if err := rows.Scan(&pref.UserID, &pref.Key, &pref.Value, &pref.UpdatedAt); err != nil {
			return nil, mapError("scan user preference", err)
		}
		prefs = append(prefs, pref)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list user preferences", err)
	}
	return prefs, nil
}

func (s *userPreferenceStore) Set(ctx context.Context, pref domain.UserPreference) (domain.UserPreference, error) {
	if len(pref.Value) == 0 {
		pref.Value = nullValue
	}
	_, err := s.q.Exec(ctx,
		`INSERT INTO user_preferences (user_id, key, value, updated_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, key) DO UPDATE
		    SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		string(pref.UserID), pref.Key, pref.Value, pref.UpdatedAt)
	if err != nil {
		return domain.UserPreference{}, mapError("set user preference", err)
	}
	return pref, nil
}

func (s *userPreferenceStore) Delete(ctx context.Context, userID domain.UserID, key string) error {
	tag, err := s.q.Exec(ctx,
		`DELETE FROM user_preferences WHERE user_id = $1 AND key = $2`, string(userID), key)
	if err != nil {
		return mapError("delete user preference", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "preference not set")
	}
	return nil
}
