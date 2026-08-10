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

// nodeMetadataStore is the PostgreSQL contracts.NodeMetadataStore: one stored
// provider answer per node (platform#62).
type nodeMetadataStore struct {
	q queryer
}

// NewNodeMetadataStore builds a pool-backed store for the read path — a detail
// screen reads one document outside any transaction.
func NewNodeMetadataStore(pool *pgxpool.Pool) contracts.NodeMetadataStore {
	return &nodeMetadataStore{q: pool}
}

func (s *nodeMetadataStore) Upsert(ctx context.Context, m contracts.NodeMetadata) (contracts.NodeMetadata, error) {
	document := m.Document
	if len(document) == 0 {
		document = []byte(`{}`)
	}
	_, err := s.q.Exec(ctx,
		`INSERT INTO node_metadata (node_id, document, source, fetched_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (node_id) DO UPDATE
		   SET document = EXCLUDED.document, source = EXCLUDED.source,
		       fetched_at = EXCLUDED.fetched_at`,
		string(m.NodeID), document, m.Source, m.FetchedAt,
	)
	if err != nil {
		return contracts.NodeMetadata{}, mapError("upsert node metadata", err)
	}
	m.Document = document
	return m, nil
}

func (s *nodeMetadataStore) Get(ctx context.Context, nodeID v1.NodeID) (contracts.NodeMetadata, error) {
	row := s.q.QueryRow(ctx,
		`SELECT node_id, document, source, fetched_at FROM node_metadata WHERE node_id = $1`,
		string(nodeID))

	var (
		m  contracts.NodeMetadata
		id string
	)
	if err := row.Scan(&id, &m.Document, &m.Source, &m.FetchedAt); err != nil {
		if isNoRows(err) {
			// Never enriched, which is not the same as enriched-and-empty: one
			// says the provider was never asked and the other says it answered
			// with nothing. A caller renders what the node carries either way,
			// and only the first is worth a run of the pass.
			return contracts.NodeMetadata{}, contracts.NewError(contracts.NotFound, "no stored metadata for this node")
		}
		return contracts.NodeMetadata{}, mapError("get node metadata", err)
	}
	m.NodeID = v1.NodeID(id)
	return m, nil
}
