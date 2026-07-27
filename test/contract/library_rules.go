// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contract

import (
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The library-rule contract (ADR 0104) and the paged, counted library read that
// the Library screen is built on (roadmap M2.1).
//
// Both are here rather than in an adapter's own tests because both carry
// behaviour an implementation could satisfy structurally and get wrong in
// practice: a count that filters differently from the page it labels, and a
// paged read whose order is not total, so a row appears on two pages and
// another on none.

func libraryRule(id, name string, at time.Time) domain.LibraryRule {
	return domain.LibraryRule{
		ID:         domain.LibraryRuleID(id),
		Name:       name,
		Kind:       domain.LibraryRuleCollection,
		ModuleID:   "stremio",
		CatalogID:  "top",
		NativeType: "movie",
		Enabled:    true,
		CreatedBy:  "user-admin",
		CreatedAt:  at,
		UpdatedAt:  at,
	}
}

// RunLibraryRuleStoreContract exercises what the library should contain.
func RunLibraryRuleStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("create and read back a rule", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		c := ctx(t)

		rule := libraryRule("rule-1", "Trending films", now)
		rule.Bound = 25
		if _, err := d.LibraryRules.Create(c, rule); err != nil {
			t.Fatalf("Create: %v", err)
		}
		found, err := d.LibraryRules.FindByID(c, rule.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if found.Name != "Trending films" || found.Kind != domain.LibraryRuleCollection {
			t.Fatalf("FindByID returned %+v", found)
		}
		if found.CatalogID != "top" || found.NativeType != "movie" || found.Bound != 25 {
			t.Fatalf("the rule's addressing did not survive a round trip: %+v", found)
		}
		if !found.LastRun.NeverRun() {
			t.Fatal("a rule that has never run must say so, rather than reading as a run that found nothing")
		}
	})

	t.Run("find missing is not found", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		_, err := d.LibraryRules.FindByID(ctx(t), "rule-nobody")
		requireCategory(t, err, contracts.NotFound)
	})

	// A rule's name is how it is identified in a run log, so two rules cannot
	// share one — and case is not a distinction a person reading a list makes.
	t.Run("a duplicate name is a conflict, case-insensitively", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		c := ctx(t)
		if _, err := d.LibraryRules.Create(c, libraryRule("rule-1", "Trending", now)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, err := d.LibraryRules.Create(c, libraryRule("rule-2", "trending", now))
		requireCategory(t, err, contracts.Conflict)
	})

	t.Run("list is oldest first and can narrow to the enabled", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		c := ctx(t)

		first := libraryRule("rule-1", "First", now.Add(-2*time.Hour))
		second := libraryRule("rule-2", "Second", now.Add(-time.Hour))
		second.Enabled = false
		third := libraryRule("rule-3", "Third", now)
		for _, r := range []domain.LibraryRule{first, second, third} {
			if _, err := d.LibraryRules.Create(c, r); err != nil {
				t.Fatalf("Create %s: %v", r.Name, err)
			}
		}

		all, err := d.LibraryRules.List(c, domain.LibraryRuleFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := ruleNames(all); !equalStrings(got, []string{"First", "Second", "Third"}) {
			t.Fatalf("List order = %v, want the order they were written in", got)
		}

		enabled, err := d.LibraryRules.List(c, domain.LibraryRuleFilter{EnabledOnly: true})
		if err != nil {
			t.Fatalf("List enabled: %v", err)
		}
		if got := ruleNames(enabled); !equalStrings(got, []string{"First", "Third"}) {
			t.Fatalf("enabled rules = %v, want the two that are on", got)
		}
	})

	// The property this exists for: an edit says what the rule *is*, and must
	// not throw away what it last *did* — which is the only account of why
	// something is in the library.
	t.Run("an edit leaves the last run alone", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		c := ctx(t)

		rule := libraryRule("rule-1", "Trending", now)
		if _, err := d.LibraryRules.Create(c, rule); err != nil {
			t.Fatalf("Create: %v", err)
		}
		run := domain.LibraryRuleRun{At: now, Matched: 40, Created: 12, Refreshed: 26, Skipped: 1, Failed: 1}
		if err := d.LibraryRules.RecordRun(c, rule.ID, run); err != nil {
			t.Fatalf("RecordRun: %v", err)
		}

		rule.Enabled = false
		rule.UpdatedAt = now.Add(time.Minute)
		if _, err := d.LibraryRules.Update(c, rule); err != nil {
			t.Fatalf("Update: %v", err)
		}

		found, err := d.LibraryRules.FindByID(c, rule.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if found.Enabled {
			t.Fatal("the edit did not take")
		}
		if found.LastRun.Created != 12 || found.LastRun.Refreshed != 26 ||
			found.LastRun.Skipped != 1 || found.LastRun.Failed != 1 || found.LastRun.Matched != 40 {
			t.Fatalf("the edit discarded the account of the last run: %+v", found.LastRun)
		}
		if found.LastRun.NeverRun() {
			t.Fatal("the edit discarded when the rule last ran")
		}
	})

	t.Run("updating a rule that is gone is not found", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		_, err := d.LibraryRules.Update(ctx(t), libraryRule("rule-nobody", "Nothing", now))
		requireCategory(t, err, contracts.NotFound)
	})

	// Withdrawing a statement mid-run is a legitimate thing for an
	// administrator to do, and the run that was already going must not fail
	// because of it.
	t.Run("recording a run against a rule that is gone is not an error", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		if err := d.LibraryRules.RecordRun(ctx(t), "rule-withdrawn", domain.LibraryRuleRun{At: now}); err != nil {
			t.Fatalf("RecordRun against a deleted rule: %v", err)
		}
	})

	t.Run("delete is idempotent", func(t *testing.T) {
		d := newDeps(t)
		if d.LibraryRules == nil {
			t.Skip("no library rule store wired")
		}
		c := ctx(t)
		rule := libraryRule("rule-1", "Trending", now)
		if _, err := d.LibraryRules.Create(c, rule); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := d.LibraryRules.Delete(c, rule.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := d.LibraryRules.Delete(c, rule.ID); err != nil {
			t.Fatalf("Delete of an already-deleted rule: %v", err)
		}
		if _, err := d.LibraryRules.FindByID(c, rule.ID); err == nil {
			t.Fatal("the rule survived being deleted")
		}
	})
}

// RunLibraryBrowseContract exercises the paged, counted read the Library screen
// is built on.
func RunLibraryBrowseContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Seven works, deliberately including two that share a title: a title-only
	// order ties on them, and a tie under LIMIT/OFFSET is how a row lands on two
	// pages while another lands on none.
	seed := func(t *testing.T, d Deps) {
		t.Helper()
		c := ctx(t)
		titles := []string{"Arrival", "Blade Runner", "Blade Runner", "Contact", "Dune", "Edge of Tomorrow", "Fargo"}
		for i, title := range titles {
			if _, err := d.Nodes.Create(c, newWork(nodeID(i+1), v1.MediaMovie, title, now)); err != nil {
				t.Fatalf("Create %s: %v", title, err)
			}
		}
		// One episode below one of them, to prove the browse counts *works* and
		// not every node in the graph — a household owns a series, not
		// sixty-four episodes of it.
		series := newWork(nodeID(50), v1.MediaTVSeries, "Severance", now)
		if _, err := d.Nodes.Create(c, series); err != nil {
			t.Fatalf("Create series: %v", err)
		}
		ep := newItem(nodeID(51), series.ID, series.ID, v1.MediaTVSeries, v1.ItemEpisode, "Half Loop", 1, now)
		if _, err := d.Nodes.Create(c, ep); err != nil {
			t.Fatalf("Create episode: %v", err)
		}
	}

	t.Run("count matches the filter, not the page", func(t *testing.T) {
		d := newDeps(t)
		seed(t, d)
		c := ctx(t)

		total, err := d.Nodes.Count(c, contracts.NodeQuery{Kind: v1.NodeWork})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if total != 8 {
			t.Fatalf("Count(works) = %d, want 8 — the episode is not a work", total)
		}

		films, err := d.Nodes.Count(c, contracts.NodeQuery{Kind: v1.NodeWork, MediaType: v1.MediaMovie})
		if err != nil {
			t.Fatalf("Count(films): %v", err)
		}
		if films != 7 {
			t.Fatalf("Count(films) = %d, want 7", films)
		}

		// The count ignores the limit, which is the whole reason it exists: a
		// page of three over eight titles must still be able to say eight.
		limited, err := d.Nodes.Count(c, contracts.NodeQuery{Kind: v1.NodeWork, Limit: 3})
		if err != nil {
			t.Fatalf("Count with a limit: %v", err)
		}
		if limited != total {
			t.Fatalf("Count with a limit = %d, want the unlimited %d", limited, total)
		}
	})

	t.Run("paging covers every row exactly once", func(t *testing.T) {
		d := newDeps(t)
		seed(t, d)
		c := ctx(t)

		var seen []string
		for offset := 0; offset < 8; offset += 3 {
			page, err := d.Nodes.Search(c, contracts.NodeQuery{Kind: v1.NodeWork, Limit: 3, Offset: offset})
			if err != nil {
				t.Fatalf("Search(offset=%d): %v", offset, err)
			}
			seen = append(seen, titles(page)...)
		}
		want := []string{
			"Arrival", "Blade Runner", "Blade Runner", "Contact",
			"Dune", "Edge of Tomorrow", "Fargo", "Severance",
		}
		if !equalStrings(seen, want) {
			t.Fatalf("paging produced %v, want each row once in title order: %v", seen, want)
		}
	})

	t.Run("an offset past the end is an empty page, not an error", func(t *testing.T) {
		d := newDeps(t)
		seed(t, d)
		page, err := d.Nodes.Search(ctx(t), contracts.NodeQuery{Kind: v1.NodeWork, Limit: 10, Offset: 500})
		if err != nil {
			t.Fatalf("Search past the end: %v", err)
		}
		if len(page) != 0 {
			t.Fatalf("Search past the end returned %d rows", len(page))
		}
	})

	// A stale link carrying page -1 is an ordinary thing to follow, and it must
	// render the first page rather than fail the screen.
	t.Run("a negative offset is the first page", func(t *testing.T) {
		d := newDeps(t)
		seed(t, d)
		page, err := d.Nodes.Search(ctx(t), contracts.NodeQuery{Kind: v1.NodeWork, Limit: 2, Offset: -5})
		if err != nil {
			t.Fatalf("Search with a negative offset: %v", err)
		}
		if got := titles(page); !equalStrings(got, []string{"Arrival", "Blade Runner"}) {
			t.Fatalf("Search(offset=-5) = %v, want the first page", got)
		}
	})

	t.Run("count and search agree on a title filter", func(t *testing.T) {
		d := newDeps(t)
		seed(t, d)
		c := ctx(t)
		query := contracts.NodeQuery{Kind: v1.NodeWork, Title: "blade", Limit: 50}

		total, err := d.Nodes.Count(c, query)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		found, err := d.Nodes.Search(c, query)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if total != len(found) {
			t.Fatalf("Count = %d but Search returned %d — the count labels a page it does not describe", total, len(found))
		}
		if total != 2 {
			t.Fatalf("Count(title~blade) = %d, want 2", total)
		}
	})
}

func ruleNames(rules []domain.LibraryRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, strings.TrimSpace(r.Name))
	}
	return out
}
