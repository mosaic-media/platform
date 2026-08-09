// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"fmt"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// The resolution register's application surface (ADR 0119).
//
// Three entry points and one internal one, and the split matters: **raising is
// not a caller's act.** A finding is created by the code that detected it,
// which is a boot path, a background job or the Supervisor's spool — none of
// which has a user. So RaiseIssue takes no Caller and goes through no policy
// gate, while reading the register and acting on a finding are ordinary
// authorised operations like everything else.

const (
	// ActionFindingsRead is evaluated for reading the register.
	ActionFindingsRead policy.Action = "findings.read"
	// ActionFindingsResolve is evaluated for applying a suggestion.
	//
	// Separate from reading because they are different powers: seeing that an
	// extension will not start is diagnostic, and uninstalling it is a change
	// to what this install is.
	ActionFindingsResolve policy.Action = "findings.resolve"
)

var findingsResource = policy.Resource{Type: "findings"}

// ListIssuesQuery reads the open register.
type ListIssuesQuery struct {
	CallerSessionID domain.SessionID
}

// ListIssuesResult is what is wrong with this install, now.
type ListIssuesResult struct {
	Issues []domain.Issue
}

// ListIssues implements the query order.
func (s *Service) ListIssues(ctx context.Context, query ListIssuesQuery) (ListIssuesResult, error) {
	if query.CallerSessionID == "" {
		return ListIssuesResult{}, contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if _, err := s.enterSession(ctx, query.CallerSessionID, ActionFindingsRead, findingsResource); err != nil {
		return ListIssuesResult{}, err
	}
	if s.issues == nil {
		// No store: the register is empty rather than broken. A build without
		// one is a test service, and answering "nothing is wrong" is both true
		// of it and the only answer it can give.
		return ListIssuesResult{}, nil
	}
	issues, err := s.issues.List(ctx)
	if err != nil {
		return ListIssuesResult{}, err
	}
	return ListIssuesResult{Issues: issues}, nil
}

// ApplySuggestionCommand acts on a finding.
type ApplySuggestionCommand struct {
	CallerSessionID domain.SessionID
	IssueID         string
	Suggestion      domain.SuggestionType
}

// ApplySuggestionResult reports what the act left behind.
type ApplySuggestionResult struct {
	// Cleared says the Issue is gone. False means the suggestion ran and the
	// situation is still true — a reinstall that failed again — which is a
	// different thing to tell somebody than success.
	Cleared bool
}

// ApplySuggestion runs a named action against an Issue.
//
// **It is an ordinary authorised command** (ADR 0119): repair is not a
// privileged back channel, and applying a suggestion goes through the same gate
// as any other change to this install.
//
// The suggestion is checked against what *this build* offers for that issue
// type rather than against what the row was stored with. A client holding a
// screen from before an upgrade would otherwise be able to ask for an action
// that has since been withdrawn, and be told it worked.
func (s *Service) ApplySuggestion(ctx context.Context, cmd ApplySuggestionCommand) (ApplySuggestionResult, error) {
	if cmd.CallerSessionID == "" {
		return ApplySuggestionResult{}, contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.IssueID == "" {
		return ApplySuggestionResult{}, contracts.NewError(contracts.InvalidArgument, "issue id is required")
	}
	if _, err := s.enterSession(ctx, cmd.CallerSessionID, ActionFindingsResolve, findingsResource); err != nil {
		return ApplySuggestionResult{}, err
	}
	if s.issues == nil {
		return ApplySuggestionResult{}, contracts.NewError(contracts.Unavailable, "this build keeps no resolution register")
	}

	issue, err := s.issues.Get(ctx, cmd.IssueID)
	if err != nil {
		return ApplySuggestionResult{}, err
	}
	if !offers(issue.Type, cmd.Suggestion) {
		return ApplySuggestionResult{}, contracts.NewError(contracts.InvalidArgument,
			fmt.Sprintf("%q is not offered for a %q issue", cmd.Suggestion, issue.Type))
	}

	switch cmd.Suggestion {
	case domain.SuggestionDismiss:
		// Acknowledged. **A real answer rather than a cop-out**: "I know, and I
		// am choosing to live with it" is a decision, and an Issue nobody can
		// close becomes furniture nobody reads. It will be raised again if the
		// situation is detected again, which is the correct behaviour — a
		// dismissal is about the finding, not a promise about the future.
		if err := s.issues.Clear(ctx, issue.ID); err != nil {
			return ApplySuggestionResult{}, err
		}
		return ApplySuggestionResult{Cleared: true}, nil

	case domain.SuggestionUninstallExtension:
		if s.extensions == nil {
			return ApplySuggestionResult{}, contracts.NewError(contracts.Unavailable,
				"this build manages no extension modules")
		}
		if err := s.extensions.Uninstall(ctx, issue.Reference); err != nil {
			return ApplySuggestionResult{}, err
		}
		// The situation rather than the row: the module is gone, so the finding
		// about it is no longer true whichever row happened to say so.
		if err := s.issues.ClearSituation(ctx, issue.Type, issue.Context, issue.Reference); err != nil {
			return ApplySuggestionResult{}, err
		}
		return ApplySuggestionResult{Cleared: true}, nil

	case domain.SuggestionReinstallExtension:
		return ApplySuggestionResult{}, contracts.NewError(contracts.Unavailable,
			"reinstalling from a finding is not built: the record names the module but not "+
				"the repository it came from, and installing from a repository a client "+
				"chose would put the trust decision in the wrong place")

	default:
		// Unreachable while offers() gates on the same set, and worth keeping:
		// the two lists drifting is exactly how a control comes to do nothing.
		return ApplySuggestionResult{}, contracts.NewError(contracts.InvalidArgument, "unknown suggestion")
	}
}

func offers(t domain.IssueType, suggestion domain.SuggestionType) bool {
	for _, offered := range domain.SuggestionsFor(t) {
		if offered == suggestion {
			return true
		}
	}
	return false
}

// RaiseIssue records a finding.
//
// **It takes no Caller and passes no policy gate, and that is deliberate.** A
// finding is created at the point of detection by the code that detected it —
// a boot path adopting extensions, a spool handed over by the Supervisor — and
// none of those is somebody's request. Requiring a principal would mean either
// inventing one or not recording the findings that matter most, which are
// exactly the ones made when nobody is asking for anything.
//
// It is unexported-by-convention rather than by name: it is on Service so the
// composition root can hand detectors something to call, and there is no
// transport that reaches it.
func (s *Service) RaiseIssue(ctx context.Context, issue domain.Issue) error {
	if s.issues == nil {
		// **Reported, not swallowed.** This returned nil for a while, and the
		// consequence was a boot that adopted two findings from the Supervisor,
		// logged "adopted findings count=2", and wrote none of them — because
		// the composition root had never passed the store in. Nothing was red
		// anywhere; the register was simply always empty. A detector is free to
		// ignore this error (they all do, on the boot path), but it must be
		// possible to see.
		return contracts.NewError(contracts.Unavailable, "this build keeps no resolution register")
	}
	if !issue.Type.Valid() {
		// Refused rather than stored. The type is a closed vocabulary because
		// code branches on it, and a row carrying one nothing knows would be a
		// finding no client can render and no suggestion can be offered for.
		return contracts.NewError(contracts.InvalidArgument,
			fmt.Sprintf("%q is not an issue type this build knows", issue.Type))
	}
	now := s.clock.Now()
	if issue.ID == "" {
		issue.ID = string(s.ids.NewID())
	}
	if issue.FirstSeen.IsZero() {
		issue.FirstSeen = now
	}
	if issue.LastSeen.IsZero() {
		issue.LastSeen = now
	}
	_, err := s.issues.Raise(ctx, issue)
	return err
}

// ClearIssueSituation withdraws a finding because the situation it described is
// no longer true. Same terms as RaiseIssue: it is the detector's counterpart,
// so it takes no Caller either.
//
// **Unlike RaiseIssue, a missing register is success here**, and the asymmetry
// is the point: raising into nothing loses information, and withdrawing from
// nothing loses none. Erroring would mean every successful boot of a build with
// no register logged a failure to withdraw a finding that was never made.
func (s *Service) ClearIssueSituation(ctx context.Context, t domain.IssueType, c domain.IssueContext, reference string) error {
	if s.issues == nil {
		return nil
	}
	return s.issues.ClearSituation(ctx, t, c, reference)
}
