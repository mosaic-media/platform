// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The Problems panel is what makes the register land at all (platform#74), so what
// is worth testing is what a person reads — not that a list rendered.

func findingsText(t *testing.T, svc *Service) string {
	t.Helper()
	return treeStrings(render(t, svc, "settings", map[string]any{"section": sectionFindings}))
}

// TestAnEmptyRegisterSaysNothingIsWrong pins that an empty register says so. A
// panel that drew nothing in the ordinary state of a working install reads as
// one that failed to load, which would teach somebody to distrust the one
// screen that has to be believed on the day it is not empty.
func TestAnEmptyRegisterSaysNothingIsWrong(t *testing.T) {
	fake := &fakeQueries{}
	svc := configService(fake)

	text := findingsText(t, svc)
	if !strings.Contains(text, "Nothing is wrong") {
		t.Errorf("an empty register drew no reassurance: %s", text)
	}
}

// TestAFindingReadsAsASentence pins that a finding reads as a sentence about
// the install, built from the type and the reference — never from the stored
// error, which may be unreadable.
func TestAFindingReadsAsASentence(t *testing.T) {
	fake := &fakeQueries{}
	svc := configService(fake)
	now := svc.now()
	fake.issues = []domain.Issue{{
		ID: "issue-1", Type: domain.IssueExtensionUnavailable,
		Context: domain.ContextExtension, Reference: "stremio",
		Source: domain.SourcePlatform, Detail: "exec format error",
		FirstSeen: now.Add(-50 * time.Hour), LastSeen: now, Occurrences: 6,
		Suggestions: domain.SuggestionsFor(domain.IssueExtensionUnavailable),
	}}

	text := findingsText(t, svc)
	for _, want := range []string{
		"The stremio module is not running",
		// Since when, and how often. One failure and a fortnight of them are
		// different problems and the row would otherwise read identically.
		"2 days ago",
		"6 times since",
		// The underlying error is context, not the meaning.
		"exec format error",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the finding does not say %q: %s", want, text)
		}
	}
}

// TestASupervisorFindingSaysSo pins that which process detected it is on the
// row, because "the Supervisor could not start the Platform" and "the Platform
// could not start a module" are different situations with different remedies.
func TestASupervisorFindingSaysSo(t *testing.T) {
	fake := &fakeQueries{}
	svc := configService(fake)
	fake.issues = []domain.Issue{{
		ID: "issue-2", Type: domain.IssueGenerationRolledBack,
		Context: domain.ContextGeneration, Reference: "v0.4.0",
		Source: domain.SourceSupervisor, FirstSeen: svc.now(), LastSeen: svc.now(), Occurrences: 1,
		Suggestions: domain.SuggestionsFor(domain.IssueGenerationRolledBack),
	}}

	text := findingsText(t, svc)
	if !strings.Contains(text, "The update to v0.4.0 did not work, and was undone") {
		t.Errorf("a rollback does not read as one: %s", text)
	}
	if !strings.Contains(text, "reported by the Supervisor") {
		t.Errorf("the finding does not say who detected it: %s", text)
	}
}

// TestEverySuggestionOfferedHasWords pins that every suggestion this build
// offers is drawable. A type with no words would render an unlabelled button, so
// the panel drops it silently — and that drop must never be reachable from what
// SuggestionsFor actually returns.
func TestEverySuggestionOfferedHasWords(t *testing.T) {
	for _, issueType := range domain.KnownIssueTypes {
		for _, suggestion := range domain.SuggestionsFor(issueType) {
			label, _, ok := suggestionControl(suggestion)
			if suggestion == domain.SuggestionReinstallExtension {
				// Deliberately undrawn: the service refuses it, so a control
				// would fail every time it was pressed (platform#24).
				if ok {
					t.Errorf("%s is drawn as a control and the service refuses it", suggestion)
				}
				continue
			}
			if !ok || label == "" {
				t.Errorf("%s is offered for a %s issue and this panel has no words for it, "+
					"so the control is silently dropped", suggestion, issueType)
			}
		}
	}
}

// TestAnUnknownIssueTypeNamesItself pins that a type from a newer build says so
// rather than drawing a blank row. The store CHECK-constrains the set, so this
// is only reachable by a downgrade — which is exactly when somebody needs to be
// told the reader is the stale one.
func TestAnUnknownIssueTypeNamesItself(t *testing.T) {
	got := issueHeadline(domain.Issue{Type: domain.IssueType("disk_full")})
	if !strings.Contains(got, "disk_full") {
		t.Errorf("an unknown type does not name itself: %q", got)
	}
}
