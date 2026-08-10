// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"
	"fmt"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The library rules, as a client can reach them (platform#60, roadmap M2.2).
//
// Four dispatch cases, and one thing they share that the account cases do not:
// **each returns to the rules list rather than to the panel it was invoked
// from.** The confirmation that creates a rule has served its purpose the moment
// the rule exists, and re-rendering the current route would show the "what this
// will do" panel again for a rule that now does it. So these actions produce
// their own surface — the toast, and the list — the way playPart produces a
// player, instead of relying on the transport's re-render of wherever the caller
// happened to be.

// libraryRuleEnvelope is what the library-rule actions carry.
//
// The catalog fields come from the action rather than from a form's scope,
// because they are *which rule this is* rather than something the form
// collects; the name is the one field a person types. That split is contracts#19's
// merge rule and it is the same shape the new-account form uses for its preset.
type libraryRuleEnvelope struct {
	// Creating.
	Name       string `json:"ruleName"`
	ModuleID   string `json:"addModule"`
	CatalogID  string `json:"addCatalog"`
	NativeType string `json:"nativeType"`
	Bound      int    `json:"bound"`

	// Managing an existing one.
	RuleID  string `json:"ruleId"`
	Enabled bool   `json:"enabled"`
}

// createLibraryRule stores what the library should contain, and returns the
// list with it on.
func (h *Handler) createLibraryRule(ctx context.Context, s *liveSession, caller v1.Caller, input []byte) ([]*sessionv1.ServerMessage, error) {
	env, err := libraryRuleFromInput(input, "create library rule")
	if err != nil {
		return nil, err
	}
	res, err := h.svc.CreateLibraryRule(ctx, app.CreateLibraryRuleCommand{
		Caller: caller,
		Name:   env.Name,
		// Only collection rules have a client path today. A saved provider
		// search is the other half of platform#60's two kinds and it is stored,
		// evaluated and run by the same code — what it lacks is a surface to
		// create one from, which is a gap in the client path rather than in the
		// mechanism.
		Kind:       domain.LibraryRuleCollection,
		ModuleID:   env.ModuleID,
		CatalogID:  env.CatalogID,
		NativeType: env.NativeType,
		Bound:      env.Bound,
	})
	if err != nil {
		// A name already in use belongs on the field that carries it rather
		// than in a toast beside a form with one box in it (contracts#13).
		if contracts.CategoryOf(err) == contracts.Conflict {
			return nil, contracts.RejectFields("That rule could not be created.",
				contracts.FieldRejection{Field: "ruleName", Message: "There is already a rule with that name."})
		}
		return nil, err
	}
	return h.libraryRulesSurface(ctx, s, fmt.Sprintf(
		"Following %q. The next maintenance run fills it in.", res.Rule.Name)), nil
}

// setLibraryRuleEnabled pauses or resumes a rule.
func (h *Handler) setLibraryRuleEnabled(ctx context.Context, s *liveSession, caller v1.Caller, input []byte) ([]*sessionv1.ServerMessage, error) {
	env, err := libraryRuleFromInput(input, "pause library rule")
	if err != nil {
		return nil, err
	}
	res, err := h.svc.SetLibraryRuleEnabled(ctx, app.SetLibraryRuleEnabledCommand{
		Caller: caller, RuleID: domain.LibraryRuleID(env.RuleID), Enabled: env.Enabled,
	})
	if err != nil {
		return nil, err
	}
	message := fmt.Sprintf("%q is paused. Nothing it added has been removed.", res.Rule.Name)
	if env.Enabled {
		message = fmt.Sprintf("%q is running again.", res.Rule.Name)
	}
	return h.libraryRulesSurface(ctx, s, message), nil
}

// deleteLibraryRule withdraws a statement and keeps everything it materialised.
func (h *Handler) deleteLibraryRule(ctx context.Context, s *liveSession, caller v1.Caller, input []byte) ([]*sessionv1.ServerMessage, error) {
	env, err := libraryRuleFromInput(input, "delete library rule")
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteLibraryRule(ctx, app.DeleteLibraryRuleCommand{
		Caller: caller, RuleID: domain.LibraryRuleID(env.RuleID),
	}); err != nil {
		return nil, err
	}
	// The sentence matters as much as the act. Deleting a rule that pulled in
	// two hundred titles deletes no titles, and somebody about to press it
	// should be told that afterwards as well as before.
	return h.libraryRulesSurface(ctx, s,
		"The rule is gone. Everything it added is still in your library."), nil
}

// runLibraryMaintenance does the scheduled pass early, on request.
//
// It is synchronous, and that is a real limit rather than an oversight: the pass
// is bounded, but a bound of two hundred items over a slow source is longer than
// a click should take, and the caller waits. The honest fix is to enqueue the
// same job kind and let the runner claim it, which needs an Enqueue reachable
// from inside a command — the seam the jobs store deliberately left open rather
// than pretending. Until then this is the button, and the schedule is what makes
// it unnecessary.
func (h *Handler) runLibraryMaintenance(ctx context.Context, s *liveSession, caller v1.Caller) ([]*sessionv1.ServerMessage, error) {
	res, err := h.svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller})
	if err != nil {
		return nil, err
	}
	return h.libraryRulesSurface(ctx, s, libraryRunToast(res)), nil
}

// libraryRunToast says what the run did, in the terms the rules themselves
// report.
func libraryRunToast(res app.RunLibraryMaintenanceResult) string {
	if res.Rules == 0 {
		return "There are no rules to run yet."
	}
	out := fmt.Sprintf("%d %s: %d added, %d refreshed, %d skipped, %d failed.",
		res.Rules, plural(res.Rules, "rule"), res.Created, res.Refreshed, res.Skipped, res.Failed)
	if res.Exhausted {
		out += " The run's budget was spent; the next one continues."
	}
	return out
}

// libraryRulesSurface is the toast and the freshly rendered rules list, and it
// moves the session's route there.
//
// Setting the route matters as much as pushing the tree: without it a reconnect
// would re-declare whichever panel the action was invoked from, and somebody who
// had just created a rule would come back to the confirmation asking whether to
// create it.
func (h *Handler) libraryRulesSurface(ctx context.Context, s *liveSession, message string) []*sessionv1.ServerMessage {
	params := map[string]any{"section": "library"}
	s.setCurrent(route{screen: "settings", params: params})

	msgs := []*sessionv1.ServerMessage{toastMsg(message, "success")}
	node, err := h.screens.Render(ctx, "settings", s.currentCaller(), params)
	if err != nil {
		// The write succeeded; only the re-render did not. Saying so beats
		// leaving the panel that was on screen, which is now describing a state
		// that no longer exists.
		return append(msgs, regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, errorNode(err.Error())))
	}
	return append(msgs, regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, node))
}

func libraryRuleFromInput(input []byte, what string) (libraryRuleEnvelope, error) {
	var env libraryRuleEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return libraryRuleEnvelope{}, contracts.NewError(contracts.InvalidArgument, what+": input is not valid JSON")
		}
	}
	return env, nil
}

// plural is the transport's own copy of the smallest possible helper, because a
// toast is a sentence and "1 rules" is the kind of thing that gets noticed.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
