// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"

	"google.golang.org/protobuf/encoding/protojson"

	sduiv1 "github.com/mosaic-media/sdui/gen/mosaic/sdui/v1"
	sdui "github.com/mosaic-media/sdui/sdui"
	"github.com/mosaic-media/sdui/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// settingsScreen hosts a module's own contributed settings UI (ADR 0038). The
// Platform owns the frame; the module fills it — the settings screen renders the
// UINode tree the module returned through ModuleSettingsUI, validated by the app
// service. It takes a moduleId param, defaulting to the Stremio module (the only
// one that provides a settings UI today); a settings index over several modules
// is a later addition.
func (s *Service) settingsScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, paramModuleID)
	if moduleID == "" {
		moduleID = defaultSettingsModule
	}
	res, err := s.content.ModuleSettingsUI(ctx, app.ModuleSettingsUIQuery{Caller: caller, ModuleID: moduleID})
	if err != nil {
		return nil, err
	}
	// The module returns its settings UI as a UINode; decode it into the typed
	// node (protojson, since the tree is now protobuf — ADR 0044).
	node := ui.Component("").Build()
	if err := protojson.Unmarshal(res.UI, node); err != nil {
		return nil, contracts.WrapError(contracts.Internal, "decode module settings UI", err)
	}

	// The expert-mode section, appended only for a caller who could actually
	// use what it reveals.
	//
	// This is the visibility rule ADR 0058 now carries: a normal user does not
	// see the toggle at all, rather than seeing it and being denied the data.
	// The record originally had it the other way round, which means routinely
	// showing people a control that fails — and a control that fails teaches
	// them the product is broken, not that they lack a permission.
	//
	// It is a hint, never a gate. The three screens behind it each authorise
	// telemetry.read for themselves, so navigating straight to one without the
	// grant is refused regardless of what was drawn here.
	if !s.content.CallerCan(ctx, caller, app.ActionTelemetryRead, "telemetry") {
		return node, nil
	}

	// Composed as protobuf rather than through the ui builder: ui.El's method
	// is unexported, so an already-built Node — which is what the module
	// returned — cannot be placed back into a builder tree from this package.
	// Appending children directly is the honest way to combine a node someone
	// else produced with one built here.
	// Whether expert mode is *on* is the preference; whether the toggle is
	// visible at all was the permission check above. Two separate questions,
	// deliberately: the permission is authority and the preference is taste.
	on := s.content.ExpertModeEnabled(ctx, caller)
	expert := expertModeSection(on).Build()
	return &sduiv1.UINode{
		Type:     "Stack",
		Children: []*sduiv1.UINode{node, expert},
	}, nil
}

// expertModeSection is the diagnostics entry point: the toggle and the links it
// governs.
//
// The toggle writes a preference and the links navigate; nothing here decides
// access. Its own visibility was decided by the caller above.
func expertModeSection(on bool) *ui.Element {
	// The toggle writes the preference and nothing else. Turning expert mode
	// off hides the diagnostics links; it does not revoke anything, and turning
	// it on grants nothing — the screens behind it authorise for themselves.
	label, style, next := "Turn on expert mode", "secondary", true
	if on {
		label, style, next = "Turn off expert mode", "ghost", false
	}
	toggle := ui.Button(label, style, ui.OnTap(ui.Invoke(setPreferenceMutation, map[string]any{
		"key":   domain.PreferenceExpertMode,
		"value": next,
	})))

	els := []ui.El{
		ui.Subtitle("Diagnostics for this install: what the Platform recorded, and where requests spent their time."),
		toggle,
	}
	if on {
		els = append(els, ui.Stack("horizontal", 8,
			ui.Button("Logs", "secondary", ui.OnTap(ui.Navigate(screenLogs, nil))),
			ui.Button("Traces", "secondary", ui.OnTap(ui.Navigate(screenTraces, nil))),
		))
	}
	return ui.Section("Expert mode", ui.Stack("vertical", 8, els...))
}
