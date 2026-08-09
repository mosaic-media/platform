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

// issueStore is the PostgreSQL contracts.IssueStore: the resolution register
// (ADR 0119).
type issueStore struct {
	q queryer
}

// NewIssueStore builds a pool-backed store for the reads and raises that happen
// outside a transaction — boot-time adoption of the Supervisor's spool, and the
// extension manager's own detection, neither of which is part of somebody's
// command.
func NewIssueStore(pool *pgxpool.Pool) contracts.IssueStore { return &issueStore{q: pool} }

const issueColumns = `id, type, context, reference, source, detail, first_seen, last_seen, occurrences`

// Raise upserts on the *situation*, not on the row.
//
// **first_seen is never moved, and that is the point of the whole statement.**
// It is the number that answers "did this start when I upgraded last week",
// which is the question a person actually has — and an upsert that touched it
// would silently make every finding look new on every boot.
func (s *issueStore) Raise(ctx context.Context, issue domain.Issue) (domain.Issue, error) {
	row := s.q.QueryRow(ctx,
		`INSERT INTO operational_issues
		   (id, type, context, reference, source, detail, first_seen, last_seen, occurrences)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1)
		 ON CONFLICT (type, context, reference) DO UPDATE
		   SET last_seen = EXCLUDED.last_seen,
		       detail = EXCLUDED.detail,
		       source = EXCLUDED.source,
		       occurrences = operational_issues.occurrences + 1
		 RETURNING `+issueColumns,
		issue.ID, string(issue.Type), string(issue.Context), issue.Reference,
		string(issue.Source), issue.Detail, issue.FirstSeen, issue.LastSeen,
	)
	stored, err := scanIssue(row)
	if err != nil {
		return domain.Issue{}, mapError("raise issue", err)
	}
	return stored, nil
}

func (s *issueStore) Clear(ctx context.Context, id string) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM operational_issues WHERE id = $1`, id)
	if err != nil {
		return mapError("clear issue", err)
	}
	if tag.RowsAffected() == 0 {
		// Reported rather than swallowed: a suggestion applied to an Issue that
		// is already gone is a client acting on a screen somebody else has
		// changed, and telling it so is what lets it re-read rather than
		// believe it did something.
		return contracts.NewError(contracts.NotFound, "issue not found")
	}
	return nil
}

func (s *issueStore) ClearSituation(ctx context.Context, issueType domain.IssueType, issueContext domain.IssueContext, reference string) error {
	// No RowsAffected check. A detector clearing a situation it has just fixed
	// is saying "this is no longer true", and that is as true when nothing had
	// recorded it as when something had — the ordinary case is an install where
	// the problem never occurred.
	_, err := s.q.Exec(ctx,
		`DELETE FROM operational_issues WHERE type = $1 AND context = $2 AND reference = $3`,
		string(issueType), string(issueContext), reference)
	if err != nil {
		return mapError("clear issue situation", err)
	}
	return nil
}

func (s *issueStore) List(ctx context.Context) ([]domain.Issue, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+issueColumns+` FROM operational_issues ORDER BY last_seen DESC, id`)
	if err != nil {
		return nil, mapError("list issues", err)
	}
	defer rows.Close()

	var out []domain.Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, mapError("scan issue", err)
		}
		out = append(out, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate issues", err)
	}
	return out, nil
}

func (s *issueStore) Get(ctx context.Context, id string) (domain.Issue, error) {
	issue, err := scanIssue(s.q.QueryRow(ctx, `SELECT `+issueColumns+` FROM operational_issues WHERE id = $1`, id))
	if err != nil {
		if isNoRows(err) {
			return domain.Issue{}, contracts.NewError(contracts.NotFound, "issue not found")
		}
		return domain.Issue{}, mapError("get issue", err)
	}
	return issue, nil
}

// scanner is the shared shape of pgx's Row and Rows, so one scan serves both.
type scanner interface{ Scan(dest ...any) error }

// scanIssue reads a row and fills in the suggestions from the type.
//
// **Derived on read rather than stored**, so a row written by an older build
// cannot pin an offer this one no longer honours — and a withdrawn suggestion
// disappears from an existing Issue instead of sitting there failing when
// pressed.
func scanIssue(row scanner) (domain.Issue, error) {
	var issue domain.Issue
	var issueType, issueContext, source string
	if err := row.Scan(&issue.ID, &issueType, &issueContext, &issue.Reference, &source,
		&issue.Detail, &issue.FirstSeen, &issue.LastSeen, &issue.Occurrences); err != nil {
		return domain.Issue{}, err
	}
	issue.Type = domain.IssueType(issueType)
	issue.Context = domain.IssueContext(issueContext)
	issue.Source = domain.IssueSource(source)
	issue.Suggestions = domain.SuggestionsFor(issue.Type)
	return issue, nil
}
