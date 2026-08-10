// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// LibraryRuleStore persists what the library should contain (platform#60).
//
// It is on Tx as well as being available as a direct read handle, for the
// reason every content store is: creating a rule emits an outbox event, and the
// two commit together or neither does. Recording a run is the one write that
// deliberately does not — see RecordRun.
type LibraryRuleStore interface {
	// Create inserts a rule. A name already in use is Conflict: an
	// administrator managing a handful of rules needs them to be tellable
	// apart, and two rules called "Trending" are indistinguishable in a run log
	// where the name is all there is.
	Create(ctx context.Context, rule domain.LibraryRule) (domain.LibraryRule, error)

	// Update replaces a rule's mutable fields, NotFound when there is no such
	// rule. It does not touch LastRun: what a rule *says* and what it last
	// *did* are written by different callers at different times, and an edit
	// that silently reset the account of the last run would lose the only
	// record of why something is in the library.
	Update(ctx context.Context, rule domain.LibraryRule) (domain.LibraryRule, error)

	// FindByID reads one rule, NotFound when there is none.
	FindByID(ctx context.Context, id domain.LibraryRuleID) (domain.LibraryRule, error)

	// List reads the rules matching filter, oldest first — the order they were
	// written in, which is the order an author remembers them in.
	List(ctx context.Context, filter domain.LibraryRuleFilter) ([]domain.LibraryRule, error)

	// Delete removes one rule. Deleting a rule removes the *statement*, never
	// the content it materialised: rules add and do not remove (platform#60), and
	// that holds when the rule itself goes away — otherwise deleting a rule
	// would be a bulk deletion of things people have half-watched.
	//
	// Deleting a rule that is already gone is not an error.
	Delete(ctx context.Context, id domain.LibraryRuleID) error

	// RecordRun stores the account of one evaluation against the rule.
	//
	// **Deliberately its own method rather than part of Update**, and
	// deliberately called outside the caller's transaction. A maintenance run
	// materialises through the ordinary import path, which opens a transaction
	// per write; there is no single transaction the run belongs to, so a run
	// record enclosed in one would either hold a lock for the length of the
	// sweep or lie about what it covered. It is last-write-wins on a row that
	// exists, and a rule deleted mid-run simply has nowhere to record — which
	// is not an error, because the statement was withdrawn while it ran.
	RecordRun(ctx context.Context, id domain.LibraryRuleID, run domain.LibraryRuleRun) error
}
