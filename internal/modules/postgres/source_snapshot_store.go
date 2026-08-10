// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// sourceSnapshotStore is the PostgreSQL contracts.SourceSnapshotStore: the last
// good answer per source and question (platform#30).
type sourceSnapshotStore struct {
	q queryer
}

// NewSourceSnapshotStore builds a pool-backed store. Pool-backed rather than
// transaction-scoped, like the playback resolution cache beside it: a snapshot
// is written on a *read* path, as a side effect of a browse that succeeded, and
// it commits with nothing.
func NewSourceSnapshotStore(pool *pgxpool.Pool) contracts.SourceSnapshotStore {
	return &sourceSnapshotStore{q: pool}
}

func (s *sourceSnapshotStore) Put(ctx context.Context, snap contracts.SourceSnapshot) error {
	document := snap.Document
	if len(document) == 0 {
		document = []byte(`[]`)
	}
	_, err := s.q.Exec(ctx,
		`INSERT INTO source_snapshots (source, key, document, taken_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (source, key) DO UPDATE
		   SET document = EXCLUDED.document, taken_at = EXCLUDED.taken_at`,
		snap.Source, snap.Key, document, snap.TakenAt,
	)
	if err != nil {
		return mapError("put source snapshot", err)
	}
	return nil
}

func (s *sourceSnapshotStore) Get(ctx context.Context, source, key string) (contracts.SourceSnapshot, error) {
	row := s.q.QueryRow(ctx,
		`SELECT source, key, document, taken_at FROM source_snapshots WHERE source = $1 AND key = $2`,
		source, key)

	var snap contracts.SourceSnapshot
	if err := row.Scan(&snap.Source, &snap.Key, &snap.Document, &snap.TakenAt); err != nil {
		if isNoRows(err) {
			// Never asked, which is the cold install: there is nothing to render
			// from, so the caller asks the source and waits. That render is the
			// only slow one, and it leaves a snapshot behind.
			return contracts.SourceSnapshot{}, contracts.NewError(contracts.NotFound, "no snapshot for this source and key")
		}
		return contracts.SourceSnapshot{}, mapError("get source snapshot", err)
	}
	return snap, nil
}
