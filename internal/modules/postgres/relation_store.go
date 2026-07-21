// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// relationStore is the PostgreSQL contracts.RelationStore. Reading a computed
// grouping back is an indexed join here, on the same engine as everything
// else, rather than a second query path.
type relationStore struct {
	q queryer
}

// NewRelationStore builds a pool-backed RelationStore for the direct (non-
// transactional) read path.
func NewRelationStore(pool *pgxpool.Pool) contracts.RelationStore {
	return &relationStore{q: pool}
}

const relationColumns = `id, from_node_id, to_node_id, relation_type, confidence, origin, created_at`

func (s *relationStore) Create(ctx context.Context, relation v1.Relation) (v1.Relation, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO relations (id, from_node_id, to_node_id, relation_type, confidence, origin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(relation.ID), string(relation.FromNodeID), string(relation.ToNodeID),
		string(relation.Type), relation.Confidence, string(relation.Origin), relation.CreatedAt,
	)
	if err != nil {
		return v1.Relation{}, mapError("create relation", err)
	}
	return relation, nil
}

func (s *relationStore) FindByID(ctx context.Context, id v1.RelationID) (v1.Relation, error) {
	row := s.q.QueryRow(ctx, `SELECT `+relationColumns+` FROM relations WHERE id = $1`, string(id))
	relation, err := scanRelation(row)
	if err != nil {
		if isNoRows(err) {
			return v1.Relation{}, contracts.NewError(contracts.NotFound, "relation not found")
		}
		return v1.Relation{}, mapError("find relation by id", err)
	}
	return relation, nil
}

func (s *relationStore) ListFrom(ctx context.Context, from v1.NodeID, relationType v1.RelationType) ([]v1.Relation, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+relationColumns+` FROM relations
		 WHERE from_node_id = $1 AND ($2 = '' OR relation_type = $2)
		 ORDER BY relation_type, created_at, id`,
		string(from), string(relationType),
	)
	if err != nil {
		return nil, mapError("list relations from node", err)
	}
	return collectRelations(rows, "list relations from node")
}

func (s *relationStore) ListTo(ctx context.Context, to v1.NodeID, relationType v1.RelationType) ([]v1.Relation, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+relationColumns+` FROM relations
		 WHERE to_node_id = $1 AND ($2 = '' OR relation_type = $2)
		 ORDER BY relation_type, created_at, id`,
		string(to), string(relationType),
	)
	if err != nil {
		return nil, mapError("list relations to node", err)
	}
	return collectRelations(rows, "list relations to node")
}

func (s *relationStore) Delete(ctx context.Context, id v1.RelationID) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM relations WHERE id = $1`, string(id))
	if err != nil {
		return mapError("delete relation", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "relation not found")
	}
	return nil
}

func scanRelation(row pgx.Row) (v1.Relation, error) {
	var (
		relation     v1.Relation
		id           string
		fromNodeID   string
		toNodeID     string
		relationType string
		origin       string
	)
	if err := row.Scan(&id, &fromNodeID, &toNodeID, &relationType,
		&relation.Confidence, &origin, &relation.CreatedAt); err != nil {
		return v1.Relation{}, err
	}
	relation.ID = v1.RelationID(id)
	relation.FromNodeID = v1.NodeID(fromNodeID)
	relation.ToNodeID = v1.NodeID(toNodeID)
	relation.Type = v1.RelationType(relationType)
	relation.Origin = v1.RelationOrigin(origin)
	return relation, nil
}

func collectRelations(rows pgx.Rows, message string) ([]v1.Relation, error) {
	defer rows.Close()

	var relations []v1.Relation
	for rows.Next() {
		relation, err := scanRelation(rows)
		if err != nil {
			return nil, mapError("scan relation row", err)
		}
		relations = append(relations, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(message, err)
	}
	return relations, nil
}
