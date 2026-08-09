// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The register's one behaviour worth pinning is the identity rule, because
// getting it wrong turns a register into a log with a table behind it — and it
// would be a working, passing, entirely wrong implementation.

func TestRaisingTheSameSituationIsOneIssue(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := postgres.NewIssueStore(pool)

	monday := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	friday := monday.Add(4 * 24 * time.Hour)

	first, err := store.Raise(ctx, domain.Issue{
		ID: "issue-1", Type: domain.IssueExtensionUnavailable,
		Context: domain.ContextExtension, Reference: "stremio",
		Source: domain.SourcePlatform, Detail: "no such file",
		FirstSeen: monday, LastSeen: monday,
	})
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if first.Occurrences != 1 {
		t.Errorf("occurrences = %d on a first raise, want 1", first.Occurrences)
	}

	// The same situation, four days later, from a different boot with a
	// different id and a different error.
	again, err := store.Raise(ctx, domain.Issue{
		ID: "issue-2", Type: domain.IssueExtensionUnavailable,
		Context: domain.ContextExtension, Reference: "stremio",
		Source: domain.SourcePlatform, Detail: "digest mismatch",
		FirstSeen: friday, LastSeen: friday,
	})
	if err != nil {
		t.Fatalf("re-raise: %v", err)
	}

	if again.ID != first.ID {
		t.Errorf("a second detection created a new issue (%s vs %s) — the register would grow "+
			"one row per boot and stop being a statement about now", again.ID, first.ID)
	}
	if again.Occurrences != 2 {
		t.Errorf("occurrences = %d after two detections, want 2", again.Occurrences)
	}
	// **first_seen is the number that answers "did this start when I updated
	// last week".** An upsert that moved it would make every finding look new
	// on every boot, which is the failure that is hardest to notice because the
	// row is otherwise perfect.
	if !again.FirstSeen.Equal(monday) {
		t.Errorf("first seen moved to %v — it must stay at the first detection", again.FirstSeen)
	}
	if !again.LastSeen.Equal(friday) {
		t.Errorf("last seen = %v, want the most recent detection", again.LastSeen)
	}
	if again.Detail != "digest mismatch" {
		t.Errorf("detail = %q, want the most recent failure's", again.Detail)
	}

	// A different reference is a different situation, not the same one.
	other, err := store.Raise(ctx, domain.Issue{
		ID: "issue-3", Type: domain.IssueExtensionUnavailable,
		Context: domain.ContextExtension, Reference: "fanart",
		Source: domain.SourcePlatform, FirstSeen: friday, LastSeen: friday,
	})
	if err != nil {
		t.Fatalf("raise other: %v", err)
	}
	if other.ID == first.ID {
		t.Error("two modules failing were folded into one finding")
	}
}

// Suggestions are derived on read, so a row written by an older build cannot
// pin an offer this one no longer honours.
func TestSuggestionsComeFromTheBuildNotTheRow(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := postgres.NewIssueStore(pool)

	stored, err := store.Raise(ctx, domain.Issue{
		ID: "issue-4", Type: domain.IssueExtensionUnavailable,
		Context: domain.ContextExtension, Reference: "stremio",
		Source: domain.SourcePlatform, FirstSeen: time.Now(), LastSeen: time.Now(),
	})
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	want := domain.SuggestionsFor(domain.IssueExtensionUnavailable)
	if len(stored.Suggestions) != len(want) {
		t.Fatalf("suggestions = %v, want this build's %v", stored.Suggestions, want)
	}

	read, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(read) == 0 || len(read[0].Suggestions) != len(want) {
		t.Error("a listed issue carries no suggestions, so a screen would draw a problem " +
			"with nothing to do about it")
	}
}

// Clearing a situation nobody recorded is a success, because a detector saying
// "this is no longer true" is as correct on an install where it never happened.
func TestClearingASituationThatWasNeverRaisedSucceeds(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := postgres.NewIssueStore(pool)
	err := store.ClearSituation(context.Background(),
		domain.IssueExtensionUnavailable, domain.ContextExtension, "never-installed")
	if err != nil {
		t.Errorf("clearing an unrecorded situation failed: %v — every successful boot would "+
			"log an error about withdrawing a finding that was never made", err)
	}
}
