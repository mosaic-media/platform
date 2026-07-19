package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	v1 "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// sourceBindingStore is the PostgreSQL contracts.SourceBindingStore.
type sourceBindingStore struct {
	q queryer
}

// NewSourceBindingStore builds a pool-backed SourceBindingStore for the
// direct (non-transactional) read path used by the review queue.
func NewSourceBindingStore(pool *pgxpool.Pool) contracts.SourceBindingStore {
	return &sourceBindingStore{q: pool}
}

const sourceBindingColumns = `id, node_id, source_provider, source_ref,
	match_confidence, match_method, status, created_at, updated_at`

func (s *sourceBindingStore) Create(ctx context.Context, binding v1.SourceBinding) (v1.SourceBinding, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO source_bindings (id, node_id, source_provider, source_ref,
		                              match_confidence, match_method, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		string(binding.ID), string(binding.NodeID), binding.SourceProvider, binding.SourceRef,
		binding.MatchConfidence, string(binding.MatchMethod), string(binding.Status),
		binding.CreatedAt, binding.UpdatedAt,
	)
	if err != nil {
		return v1.SourceBinding{}, mapError("create source binding", err)
	}
	return binding, nil
}

func (s *sourceBindingStore) FindByID(ctx context.Context, id v1.SourceBindingID) (v1.SourceBinding, error) {
	row := s.q.QueryRow(ctx, `SELECT `+sourceBindingColumns+` FROM source_bindings WHERE id = $1`, string(id))
	binding, err := scanSourceBinding(row)
	if err != nil {
		if isNoRows(err) {
			return v1.SourceBinding{}, contracts.NewError(contracts.NotFound, "source binding not found")
		}
		return v1.SourceBinding{}, mapError("find source binding by id", err)
	}
	return binding, nil
}

func (s *sourceBindingStore) FindBySource(ctx context.Context, provider, ref string) (v1.SourceBinding, error) {
	row := s.q.QueryRow(ctx,
		`SELECT `+sourceBindingColumns+` FROM source_bindings
		 WHERE source_provider = $1 AND source_ref = $2`,
		provider, ref,
	)
	binding, err := scanSourceBinding(row)
	if err != nil {
		if isNoRows(err) {
			return v1.SourceBinding{}, contracts.NewError(contracts.NotFound, "source binding not found")
		}
		return v1.SourceBinding{}, mapError("find source binding by source", err)
	}
	return binding, nil
}

// Update carries both a confirmation and a split. A split changes node_id and
// nothing else: the source keeps its identity, confidence and match method,
// and is never re-fingerprinted.
func (s *sourceBindingStore) Update(ctx context.Context, binding v1.SourceBinding) (v1.SourceBinding, error) {
	tag, err := s.q.Exec(ctx,
		`UPDATE source_bindings SET node_id = $2, source_provider = $3, source_ref = $4,
		                            match_confidence = $5, match_method = $6, status = $7, updated_at = $8
		 WHERE id = $1`,
		string(binding.ID), string(binding.NodeID), binding.SourceProvider, binding.SourceRef,
		binding.MatchConfidence, string(binding.MatchMethod), string(binding.Status), binding.UpdatedAt,
	)
	if err != nil {
		return v1.SourceBinding{}, mapError("update source binding", err)
	}
	if tag.RowsAffected() == 0 {
		return v1.SourceBinding{}, contracts.NewError(contracts.NotFound, "source binding not found")
	}
	return binding, nil
}

func (s *sourceBindingStore) ListByNode(ctx context.Context, nodeID v1.NodeID) ([]v1.SourceBinding, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+sourceBindingColumns+` FROM source_bindings
		 WHERE node_id = $1 ORDER BY created_at, id`,
		string(nodeID),
	)
	if err != nil {
		return nil, mapError("list source bindings by node", err)
	}
	return collectSourceBindings(rows, "list source bindings by node")
}

func (s *sourceBindingStore) ListPendingReview(ctx context.Context, limit int) ([]v1.SourceBinding, error) {
	if limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}
	rows, err := s.q.Query(ctx,
		`SELECT `+sourceBindingColumns+` FROM source_bindings
		 WHERE status = 'pending_review' ORDER BY created_at, id LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, mapError("list pending source bindings", err)
	}
	return collectSourceBindings(rows, "list pending source bindings")
}

func (s *sourceBindingStore) Delete(ctx context.Context, id v1.SourceBindingID) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM source_bindings WHERE id = $1`, string(id))
	if err != nil {
		return mapError("delete source binding", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "source binding not found")
	}
	return nil
}

func scanSourceBinding(row pgx.Row) (v1.SourceBinding, error) {
	var (
		binding v1.SourceBinding
		id      string
		nodeID  string
		method  string
		status  string
	)
	if err := row.Scan(&id, &nodeID, &binding.SourceProvider, &binding.SourceRef,
		&binding.MatchConfidence, &method, &status,
		&binding.CreatedAt, &binding.UpdatedAt); err != nil {
		return v1.SourceBinding{}, err
	}
	binding.ID = v1.SourceBindingID(id)
	binding.NodeID = v1.NodeID(nodeID)
	binding.MatchMethod = v1.MatchMethod(method)
	binding.Status = v1.BindingStatus(status)
	return binding, nil
}

func collectSourceBindings(rows pgx.Rows, message string) ([]v1.SourceBinding, error) {
	defer rows.Close()

	var bindings []v1.SourceBinding
	for rows.Next() {
		binding, err := scanSourceBinding(rows)
		if err != nil {
			return nil, mapError("scan source binding row", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(message, err)
	}
	return bindings, nil
}
