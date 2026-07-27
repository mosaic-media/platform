// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// libraryRuleStore is the PostgreSQL contracts.LibraryRuleStore: one row per
// statement of what the library should contain (ADR 0104).
type libraryRuleStore struct {
	q queryer
}

// NewLibraryRuleStore builds a pool-backed store for the read path — the
// settings surface listing the rules, and the maintenance job reading the
// enabled ones, both outside any transaction.
func NewLibraryRuleStore(pool *pgxpool.Pool) contracts.LibraryRuleStore {
	return &libraryRuleStore{q: pool}
}

const libraryRuleColumns = `id, name, kind, module_id, catalog_id, native_type, query_text,
	media_type, bound, enabled, created_by, created_at, updated_at,
	last_run_at, last_run_matched, last_run_created, last_run_refreshed,
	last_run_skipped, last_run_failed, last_run_error`

func (s *libraryRuleStore) Create(ctx context.Context, rule domain.LibraryRule) (domain.LibraryRule, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO library_rules (id, name, kind, module_id, catalog_id, native_type, query_text,
		                            media_type, bound, enabled, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		string(rule.ID), rule.Name, string(rule.Kind), rule.ModuleID,
		rule.CatalogID, rule.NativeType, rule.Text, rule.MediaType,
		rule.Bound, rule.Enabled, string(rule.CreatedBy), rule.CreatedAt, rule.UpdatedAt,
	)
	if err != nil {
		return domain.LibraryRule{}, mapError("create library rule", err)
	}
	return rule, nil
}

// Update writes the mutable fields and leaves the last-run columns alone. An
// edit is a change to what the rule *says*; what it last did is a separate
// fact, written by RecordRun.
func (s *libraryRuleStore) Update(ctx context.Context, rule domain.LibraryRule) (domain.LibraryRule, error) {
	tag, err := s.q.Exec(ctx,
		`UPDATE library_rules
		    SET name = $2, kind = $3, module_id = $4, catalog_id = $5, native_type = $6,
		        query_text = $7, media_type = $8, bound = $9, enabled = $10, updated_at = $11
		  WHERE id = $1`,
		string(rule.ID), rule.Name, string(rule.Kind), rule.ModuleID,
		rule.CatalogID, rule.NativeType, rule.Text, rule.MediaType,
		rule.Bound, rule.Enabled, rule.UpdatedAt,
	)
	if err != nil {
		return domain.LibraryRule{}, mapError("update library rule", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.LibraryRule{}, contracts.NewError(contracts.NotFound, "library rule not found")
	}
	return rule, nil
}

func (s *libraryRuleStore) FindByID(ctx context.Context, id domain.LibraryRuleID) (domain.LibraryRule, error) {
	row := s.q.QueryRow(ctx, `SELECT `+libraryRuleColumns+` FROM library_rules WHERE id = $1`, string(id))
	rule, err := scanLibraryRule(row)
	if err != nil {
		if isNoRows(err) {
			return domain.LibraryRule{}, contracts.NewError(contracts.NotFound, "library rule not found")
		}
		return domain.LibraryRule{}, mapError("find library rule", err)
	}
	return rule, nil
}

func (s *libraryRuleStore) List(ctx context.Context, filter domain.LibraryRuleFilter) ([]domain.LibraryRule, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+libraryRuleColumns+` FROM library_rules
		  WHERE ($1 = false OR enabled = true)
		  ORDER BY created_at, id`,
		filter.EnabledOnly,
	)
	if err != nil {
		return nil, mapError("list library rules", err)
	}
	defer rows.Close()

	var out []domain.LibraryRule
	for rows.Next() {
		rule, err := scanLibraryRule(rows)
		if err != nil {
			return nil, mapError("scan library rule", err)
		}
		out = append(out, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list library rules", err)
	}
	return out, nil
}

// Delete removes the statement and nothing it materialised. Deleting a rule
// that is already gone affects zero rows and is not an error.
func (s *libraryRuleStore) Delete(ctx context.Context, id domain.LibraryRuleID) error {
	if _, err := s.q.Exec(ctx, `DELETE FROM library_rules WHERE id = $1`, string(id)); err != nil {
		return mapError("delete library rule", err)
	}
	return nil
}

// RecordRun is last-write-wins, and a rule deleted while it ran simply has
// nowhere to record — zero rows affected, no error. The statement was withdrawn
// mid-run, which is a legitimate thing for an administrator to do and not a
// failure of the run.
func (s *libraryRuleStore) RecordRun(ctx context.Context, id domain.LibraryRuleID, run domain.LibraryRuleRun) error {
	var at any
	if !run.At.IsZero() {
		at = run.At
	}
	_, err := s.q.Exec(ctx,
		`UPDATE library_rules
		    SET last_run_at = $2, last_run_matched = $3, last_run_created = $4,
		        last_run_refreshed = $5, last_run_skipped = $6, last_run_failed = $7,
		        last_run_error = $8
		  WHERE id = $1`,
		string(id), at, run.Matched, run.Created, run.Refreshed, run.Skipped, run.Failed, run.Error,
	)
	if err != nil {
		return mapError("record library rule run", err)
	}
	return nil
}

func scanLibraryRule(row pgx.Row) (domain.LibraryRule, error) {
	var (
		rule      domain.LibraryRule
		id        string
		kind      string
		createdBy string
		lastRunAt *time.Time
	)
	if err := row.Scan(
		&id, &rule.Name, &kind, &rule.ModuleID, &rule.CatalogID, &rule.NativeType, &rule.Text,
		&rule.MediaType, &rule.Bound, &rule.Enabled, &createdBy, &rule.CreatedAt, &rule.UpdatedAt,
		&lastRunAt, &rule.LastRun.Matched, &rule.LastRun.Created, &rule.LastRun.Refreshed,
		&rule.LastRun.Skipped, &rule.LastRun.Failed, &rule.LastRun.Error,
	); err != nil {
		return domain.LibraryRule{}, err
	}
	rule.ID = domain.LibraryRuleID(id)
	rule.Kind = domain.LibraryRuleKind(kind)
	rule.CreatedBy = domain.UserID(createdBy)
	if lastRunAt != nil {
		rule.LastRun.At = *lastRunAt
	}
	return rule, nil
}
