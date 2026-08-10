// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The configuration activation state machine, as a client can reach it
// (ADR 0011, roadmap M4.4).
//
// **Three services, one action.** Draft, validate and activate are the
// machinery of the reload-class model, not a workflow: somebody changing how
// often the library pass runs is not drafting a version. Three controls would
// make an operator drive an implementation in order, and leave a half-finished
// draft behind every time they changed their mind — so this composes them, and
// keeps from the three only the thing they alone can say: whether the change
// took effect, or is waiting for something bigger than this process.

// applyConfiguration takes the fields somebody changed and puts them through
// draft, validate and activate as one act.
func (h *Handler) applyConfiguration(ctx context.Context, s *liveSession, caller v1.Caller, input []byte) ([]*sessionv1.ServerMessage, error) {
	submitted, err := configFieldsFromInput(input)
	if err != nil {
		return nil, err
	}
	if len(submitted) == 0 {
		return h.configurationSurface(ctx, s, "Nothing was changed."), nil
	}

	// A draft is the whole configuration, not a patch: the activation model
	// diffs one payload against the Active one, so a version carrying only the
	// changed fields would read as every other field being *removed* and would
	// classify against the wrong diff. So the boxes somebody filled are merged
	// over what is currently stored.
	merged, err := h.mergedConfigPayload(ctx, caller, submitted)
	if err != nil {
		return nil, err
	}

	drafted, err := h.svc.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
		CallerSessionID: domain.SessionID(caller.Session), Payload: merged,
	})
	if err != nil {
		return nil, err
	}
	validated, err := h.svc.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{
		CallerSessionID: domain.SessionID(caller.Session), ConfigVersionID: drafted.Version.ID,
	})
	if err != nil {
		return nil, err
	}
	// A rejected draft is the ordinary answer to a bad value rather than a
	// failure of the call, and the detail names the field and why. It belongs
	// on the form that carries it rather than in a toast beside it (contracts#13).
	if validated.Version.Status == domain.ConfigRejected {
		return nil, contracts.NewError(contracts.InvalidArgument,
			"That change was not accepted: "+validated.Version.ValidationDetail)
	}

	applied, err := h.svc.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{
		CallerSessionID: domain.SessionID(caller.Session), ConfigVersionID: validated.Version.ID,
	})
	if err != nil {
		return nil, err
	}
	return h.configurationSurface(ctx, s, appliedSentence(applied)), nil
}

// mergedConfigPayload lays the submitted fields over the Active version's
// payload, so a draft is a complete configuration rather than a patch.
//
// A fresh install has no Active version, and the submitted fields alone are
// then the whole configuration — which is correct rather than a fallback: there
// is nothing to preserve.
func (h *Handler) mergedConfigPayload(ctx context.Context, caller v1.Caller, submitted map[string]any) ([]byte, error) {
	merged := map[string]any{}

	active, err := h.svc.GetActiveConfigVersion(ctx, app.GetActiveConfigVersionQuery{
		CallerSessionID: domain.SessionID(caller.Session),
	})
	switch {
	case err == nil:
		if len(active.Version.Payload) > 0 {
			if err := json.Unmarshal(active.Version.Payload, &merged); err != nil {
				// An unreadable Active payload must not be silently replaced by
				// whatever this form happened to carry — that would discard
				// every field the form does not offer.
				return nil, contracts.WrapError(contracts.Internal,
					"the stored configuration could not be read", err)
			}
		}
	case contracts.CategoryOf(err) == contracts.NotFound:
		// Nothing activated yet. Normal on a fresh install.
	default:
		return nil, err
	}

	for key, value := range submitted {
		merged[key] = value
	}
	return json.Marshal(merged)
}

// appliedSentence says what happened in terms of what the person now has to do,
// which for anything but a Hot change is something.
func appliedSentence(res app.ActivateConfigVersionResult) string {
	if res.Activated {
		return "Applied."
	}
	switch res.ReloadClass {
	case config.Restart:
		return "Saved. It takes effect when the server restarts."
	case config.Generation:
		return "Saved. It takes effect when an upgrade is activated — which is not built yet."
	case config.Recovery:
		return "Saved. It can only be applied through the recovery flow."
	}
	return "Saved, and waiting to be applied."
}

// configFieldsFromInput reads the submitted form values, keeping only fields
// the schema knows and only boxes somebody actually filled.
//
// **Unknown keys are dropped rather than rejected**, and the reason is the
// form: a submit carries every variable in its scope, including the error
// binding the form itself uses. Refusing the call because a client sent
// something the schema does not name would turn a rendering detail into an
// error nobody can act on. A value that *is* a config field and is wrong is a
// different matter, and validation is where it is answered.
//
// **Numbers are parsed here rather than stored as text.** Every reader on the
// far side takes a JSON number (`durationField`, `countField`), so a retention
// of "30" stored as a string is accepted by validation — the schema checks the
// field is registered, not its type — and then silently ignored by the reader
// that was supposed to apply it. That is the worst shape this bug has: a
// configured value that reports success and never takes effect.
func configFieldsFromInput(input []byte) (map[string]any, error) {
	if len(input) == 0 {
		return nil, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(input, &raw); err != nil {
		return nil, contracts.NewError(contracts.InvalidArgument, "apply configuration: input is not valid JSON")
	}

	schema := config.PlatformSchema()
	out := make(map[string]any, len(raw))
	var rejections []contracts.FieldRejection
	for key, value := range raw {
		if _, known := schema.ReloadClassOf(key); !known {
			continue
		}
		text, ok := value.(string)
		if !ok {
			out[key] = value
			continue
		}
		if strings.TrimSpace(text) == "" {
			// An empty box means "leave it alone", not "set it to empty".
			// Writing the empty string would be a silent way to unset a value
			// nobody touched.
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			rejections = append(rejections, contracts.FieldRejection{
				Field: key, Message: "This has to be a whole number.",
			})
			continue
		}
		out[key] = n
	}
	if len(rejections) > 0 {
		return nil, contracts.RejectFields("Some of those values could not be used.", rejections...)
	}
	return out, nil
}

// configurationSurface is the toast and the freshly rendered panel, and it
// moves the session's route there.
//
// Setting the route matters as much as pushing the tree: without it a reconnect
// would re-declare whichever panel the action was invoked from — and for a
// Restart-class change the *point* is that the panel now says something new.
func (h *Handler) configurationSurface(ctx context.Context, s *liveSession, message string) []*sessionv1.ServerMessage {
	params := map[string]any{"section": "configuration"}
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
