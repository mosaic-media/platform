// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Acting on a finding (ADR 0119).
//
// **Applying a suggestion is an ordinary action on the ordinary lane.** Repair
// is not a privileged back channel: it authorises like every other command, its
// visible result arrives on the push lane like every other one, and the panel
// is re-rendered afterwards because the register it drew has changed.

// suggestionInput is what the panel's control sends.
type suggestionInput struct {
	IssueID    string `json:"issueId"`
	Suggestion string `json:"suggestion"`
}

// applySuggestion runs the named suggestion against a finding and re-renders
// the panel.
func (h *Handler) applySuggestion(ctx context.Context, s *liveSession, caller v1.Caller, input []byte) ([]*sessionv1.ServerMessage, error) {
	var in suggestionInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, contracts.NewError(contracts.InvalidArgument, "unreadable suggestion input")
		}
	}
	if in.IssueID == "" || in.Suggestion == "" {
		return nil, contracts.NewError(contracts.InvalidArgument,
			"a suggestion needs the issue it is about and the action to take")
	}

	result, err := h.svc.ApplySuggestion(ctx, app.ApplySuggestionCommand{
		CallerSessionID: domain.SessionID(caller.Session),
		IssueID:         in.IssueID,
		Suggestion:      domain.SuggestionType(in.Suggestion),
	})
	if err != nil {
		return nil, err
	}

	// **Two different sentences, because they are two different outcomes.** A
	// suggestion that ran and did not resolve the situation is not a failure —
	// nothing errored — but telling somebody "Done" when the problem is still
	// on the screen they are about to be shown is how a surface loses their
	// trust in one step.
	message := "That did not fix it — the problem is still open."
	if result.Cleared {
		message = "Sorted."
	}
	return h.findingsSurface(ctx, s, message), nil
}

// findingsSurface re-renders the Problems panel after acting on it.
func (h *Handler) findingsSurface(ctx context.Context, s *liveSession, message string) []*sessionv1.ServerMessage {
	params := map[string]any{"section": "problems"}
	s.setCurrent(route{screen: "settings", params: params})

	msgs := []*sessionv1.ServerMessage{toastMsg(message, "success")}
	node, err := h.screens.Render(ctx, "settings", s.currentCaller(), params)
	if err != nil {
		// The act succeeded; only the re-render did not. Saying so beats
		// leaving a panel on screen that is now describing a register it no
		// longer matches.
		return append(msgs, regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, errorNode(err.Error())))
	}
	return append(msgs, regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, node))
}
