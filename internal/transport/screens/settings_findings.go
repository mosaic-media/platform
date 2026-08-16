// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings › Problems — the client path to the resolution register (platform#74).
//
// The register needs a client path: a durable typed record of what is wrong,
// readable only by querying PostgreSQL, is a log line with a table behind it.
//
// The words are here and the meaning is not. A finding travels as a type, a
// context and a reference; this file turns those into sentences, and a
// suggestion into a control. A Suggestion carries no prose, which is what lets a
// second client say the same thing differently, or in another language, without
// the Platform changing.

const (
	// sectionFindings is the settings section this panel fills.
	sectionFindings = "problems"

	applySuggestionAction = "applySuggestion"
)

// findingsPanel lists what is wrong with this install, now.
func (s *Service) findingsPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel) (sdui.Node, error) {
	result, err := s.content.ListIssues(ctx, app.ListIssuesQuery{
		CallerSessionID: domain.SessionID(caller.Session),
	})
	if err != nil {
		return nil, err
	}

	body := []sdui.Node{}
	if len(result.Issues) == 0 {
		// Said plainly rather than left blank. An empty register is the ordinary
		// state of a working install, and a panel that draws nothing in it reads
		// as a panel that failed to load — which would teach somebody to distrust
		// the one screen that has to be believed on the day it is not empty.
		body = append(body, ui.EmptyState("check", "Nothing is wrong.",
			ui.Summary("Mosaic records anything it could not do here — a module that will not "+
				"start, an update that was rolled back — so it is still here when you come "+
				"looking.")).Build())
		return settingsFrame(nav, sectionFindings, "Problems", "", body...), nil
	}

	rows := make([]ui.El, 0, len(result.Issues))
	for _, issue := range result.Issues {
		rows = append(rows, findingRow(issue, s.now()))
	}
	body = append(body, ui.Section(
		fmt.Sprintf("%d to look at", len(result.Issues)),
		ui.Stack("vertical", 0, rows...)).Build())

	return settingsFrame(nav, sectionFindings, "Problems", "", body...), nil
}

// findingRow renders one finding: what it is, since when, and what can be done.
func findingRow(issue domain.Issue, now time.Time) ui.El {
	controls := make([]ui.El, 0, len(issue.Suggestions))
	for _, suggestion := range issue.Suggestions {
		label, variant, ok := suggestionControl(suggestion)
		if !ok {
			// A suggestion this build offers and this screen has no words for
			// would draw an unlabelled button. Skipping it is the lesser
			// failure, and the panel test refuses the situation outright.
			continue
		}
		controls = append(controls, ui.Button(label, variant,
			ui.OnTap(ui.Invoke(applySuggestionAction, map[string]any{
				"issueId":    issue.ID,
				"suggestion": string(suggestion),
			}))))
	}

	return ui.SettingsRow(issueHeadline(issue),
		ui.Summary(issueDetail(issue, now)),
		ui.Group(controls...))
}

// issueHeadline is the sentence a person reads first: what is wrong, about
// what. It is built from the type and the reference and never from the stored
// detail, so a finding is legible even when the underlying error is not.
func issueHeadline(issue domain.Issue) string {
	switch issue.Type {
	case domain.IssueExtensionUnavailable:
		return fmt.Sprintf("The %s module is not running", issue.Reference)
	case domain.IssueChildUnrecoverable:
		return fmt.Sprintf("Mosaic cannot start %s", issue.Reference)
	case domain.IssueGenerationRolledBack:
		return fmt.Sprintf("The update to %s did not work, and was undone", issue.Reference)
	case domain.IssueProvisionFailed:
		return "Mosaic could not download what it needs to run"
	case domain.IssueUpgradeAvailable:
		// The one headline here that is not about something being wrong. It is
		// on this panel because the register is where an install says what needs
		// a person's attention, and it reads as an offer rather than a fault so
		// nobody arrives thinking their Mosaic is broken.
		return fmt.Sprintf("Version %s is available", issue.Reference)
	case domain.IssueUpgradeFailed:
		return fmt.Sprintf("The update to %s could not be installed", issue.Reference)
	default:
		// A type this build does not know cannot reach here — the store
		// CHECK-constrains it and RaiseIssue refuses it — but a row written by
		// a newer build and read by this one can. Naming it beats drawing a
		// blank row, and it says plainly that the reader is the stale one.
		return fmt.Sprintf("A problem this version does not recognise (%s)", issue.Type)
	}
}

// issueDetail is the second line: since when, how often, and what the failure
// actually said.
//
// The count is here because one failure and a fortnight of them are different
// problems and the row would otherwise read identically. So is "first seen": it
// answers "did this start when I updated last week", which is the question
// somebody actually has.
func issueDetail(issue domain.Issue, now time.Time) string {
	detail := fmt.Sprintf("First seen %s", relativeTime(issue.FirstSeen, now))
	if issue.Occurrences > 1 {
		detail += fmt.Sprintf(", %d times since", issue.Occurrences)
	}
	if issue.Source == domain.SourceSupervisor {
		// Named, because "the Supervisor could not start the Platform" and "the
		// Platform could not start a module" are different situations with
		// different remedies, and the row would otherwise read as the second.
		detail += " · reported by the Supervisor"
	}
	if issue.Detail != "" {
		detail += " · " + issue.Detail
	}
	return detail
}

// suggestionControl turns a suggestion type into a control.
//
// This is the one place a suggestion becomes words (platform#74): the type
// carries no prose, so a second client renders the same set differently, or in
// another language, with nothing on the server changing.
func suggestionControl(s domain.SuggestionType) (label, variant string, ok bool) {
	switch s {
	case domain.SuggestionUninstallExtension:
		return "Remove it", "danger", true
	case domain.SuggestionApplyUpgrade:
		// The Platform cannot perform this and says so honestly by wording it as
		// a request: pressing it records what was asked for, and the Supervisor
		// carries it out (platform#77). "Install it" would promise an immediacy
		// that crossing a process boundary does not have.
		return "Install it", "primary", true
	case domain.SuggestionDismiss:
		return "Dismiss", "ghost", true
	case domain.SuggestionReinstallExtension:
		// Deliberately not drawn. The service refuses it — the record names the
		// module and not the repository it came from — and a control that
		// reported failure every time is worse than no control (platform#24).
		return "", "", false
	default:
		return "", "", false
	}
}

// relativeTime is a coarse "how long ago", which is what a person reads a
// finding for. An exact timestamp is in telemetry, keyed by the same moment.
func relativeTime(then, now time.Time) string {
	if then.IsZero() {
		return "at an unknown time"
	}
	switch d := now.Sub(then); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
