// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// IssueStore persists the resolution register (ADR 0119): what is wrong with
// this install, now.
//
// **Raise is idempotent on the situation, not on the detection.** A module that
// fails to start on every boot is one Issue that has been happening since
// Tuesday, not one per boot — so raising the same (type, context, reference)
// again moves LastSeen and increments Occurrences, and never moves FirstSeen.
// That is the whole difference between this and an event stream, and getting it
// wrong turns a register into a log with a table behind it.
type IssueStore interface {
	// Raise records a finding, or records that an existing one happened again.
	// It returns the stored Issue, so a caller can tell a new situation from a
	// recurring one by its Occurrences.
	Raise(ctx context.Context, issue domain.Issue) (domain.Issue, error)
	// Clear removes an Issue by id. Resolving is a delete rather than a flag:
	// the register answers "what is wrong now", and a resolved row is a fact
	// about the past, which is what telemetry already keeps.
	Clear(ctx context.Context, id string) error
	// ClearSituation removes an Issue by its identity, for the code that
	// resolves a problem without knowing the id — which is most of it, since a
	// detector knows what it fixed and not which row said so.
	ClearSituation(ctx context.Context, issueType domain.IssueType, issueContext domain.IssueContext, reference string) error
	// List returns every open Issue, most recently seen first.
	List(ctx context.Context) ([]domain.Issue, error)
	// Get returns one Issue by id.
	Get(ctx context.Context, id string) (domain.Issue, error)
}
