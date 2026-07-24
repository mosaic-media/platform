// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"

	"google.golang.org/protobuf/encoding/protojson"

	sduiv1 "github.com/mosaic-media/contracts/gen/mosaic/sdui/v1"
	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// settingsScreen hosts a module's own contributed settings UI (ADR 0038). The
// Platform owns the frame; the module fills it — the screen renders the UINode
// tree the module returned through ModuleSettingsUI, validated by the app
// service.
//
// With no moduleId it renders the **index** of modules that have a settings
// screen. That used to be a constant naming the Stremio module, which meant
// every module contributing a screen after the first had one nobody could open:
// `module-tmdb` shipped a whole credential form that way, and a stream provider
// whose only path from "installed" to "resolving" is its settings screen would
// have shipped dead on arrival. The index is the client path those capabilities
// were owed.
func (s *Service) settingsScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, paramModuleID)
	if moduleID == "" {
		return s.settingsIndexScreen(ctx, caller)
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

	// Composed as protobuf rather than through the ui builder: ui.El's method is
	// unexported, so an already-built Node — which is what the module returned —
	// cannot be placed back into a builder tree from this package. Appending
	// children directly is the honest way to combine a node someone else produced
	// with one built here.
	//
	// The frame is the Platform's and the body is the module's, which is exactly
	// the split ADR 0038 draws: a module contributes a form, never a screen, and
	// the way back out of it is not the module's to decide.
	return &sduiv1.UINode{
		Type:     "Stack",
		Children: []*sduiv1.UINode{backToSettingsIndex().Build(), node},
	}, nil
}

// settingsIndexScreen lists the modules that contribute a settings screen, and
// carries the install-level sections that belong to no module.
//
// Expert mode moved here from the module screen, where it was appended to
// whichever module happened to be the default — so install diagnostics rendered
// under a heading about Stremio addons. It is a property of the install, and now
// it sits with the other install-level things.
func (s *Service) settingsIndexScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	res, err := s.content.ListSettingsModules(ctx, app.ListSettingsModulesQuery{Caller: caller})
	if err != nil {
		return nil, err
	}

	body := []ui.El{modulesSection(res.Modules)}

	// A way into the extensions surface, shown only to a caller who can read the
	// catalogue (the same permission the screen itself authorises). Like expert
	// mode below, the visibility is the hint and the screen is the gate.
	if s.content.CallerCan(ctx, caller, app.ActionModuleRead, "extension") {
		body = append(body, ui.Section("Extensions",
			ui.Subtitle("Install and remove optional modules from the trusted repository."),
			ui.Button("Manage extensions", "secondary", ui.OnTap(ui.Navigate(screenExtensions, nil)))))
	}

	// The expert-mode section, shown only to a caller who could actually use what
	// it reveals.
	//
	// This is the visibility rule ADR 0058 now carries: a normal user does not see
	// the toggle at all, rather than seeing it and being denied the data. The
	// record originally had it the other way round, which means routinely showing
	// people a control that fails — and a control that fails teaches them the
	// product is broken, not that they lack a permission.
	//
	// It is a hint, never a gate. The three screens behind it each authorise
	// telemetry.read for themselves, so navigating straight to one without the
	// grant is refused regardless of what was drawn here. Whether expert mode is
	// *on* is the preference; whether the toggle is visible at all is this
	// permission — authority and taste, two separate questions.
	if s.content.CallerCan(ctx, caller, app.ActionTelemetryRead, "telemetry") {
		body = append(body, expertModeSection(s.content.ExpertModeEnabled(ctx, caller)))
	}

	return ui.Screen(ui.Title("Settings"), ui.Group(body...)).Build(), nil
}

// modulesSection lists each module that has a settings screen, as a row that
// opens it.
//
// The empty state is reachable: a build composed with no settings-UI module is a
// legitimate composition, and it must say so rather than render a heading with
// nothing under it.
func modulesSection(modules []app.SettingsModule) *ui.Element {
	if len(modules) == 0 {
		return ui.Section("Modules",
			ui.EmptyState(emptyIconCollections, "No module in this build contributes settings"))
	}
	rows := make([]ui.El, 0, len(modules))
	for _, m := range modules {
		rows = append(rows, ui.Button(m.Name, "secondary",
			ui.OnTap(ui.Navigate(screenSettings, map[string]any{paramModuleID: m.ModuleID}))))
	}
	return ui.Section("Modules", ui.Stack("vertical", 8, rows...))
}

// extensionsScreen is the browse-and-install surface for extension modules
// (ADR 0081): what a user has installed, with a way to remove each, and what the
// trusted repository offers, with a way to install each. It is its own screen
// because listing what is available reaches the repository over the network, so
// it happens when a user opens this rather than on every settings render.
func (s *Service) extensionsScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	installed, err := s.content.ListInstalledExtensions(ctx, app.ListInstalledExtensionsQuery{Caller: caller})
	if err != nil {
		return nil, err
	}
	installedByID := make(map[string]bool, len(installed))
	for _, e := range installed {
		installedByID[e.ModuleID] = true
	}

	body := []ui.El{backToSettingsIndex(), installedExtensionsSection(installed)}

	// Available is a network read. If the repository is unreachable, show what is
	// installed and say the catalogue could not be loaded rather than failing the
	// whole screen — a user must still be able to uninstall when the repo is down.
	available, availErr := s.content.ListAvailableExtensions(ctx, app.ListAvailableExtensionsQuery{Caller: caller})
	if availErr != nil {
		body = append(body, ui.Section("Available",
			ui.EmptyState(emptyIconCollections, "The extension repository could not be reached")))
	} else {
		body = append(body, availableExtensionsSection(available, installedByID))
	}

	return ui.Screen(ui.Title("Extensions"), ui.Group(body...)).Build(), nil
}

// installedExtensionsSection lists each installed extension with a control to
// remove it. The empty state is the ordinary one for a fresh install (ADR 0081:
// nothing is installed by default).
func installedExtensionsSection(installed []app.InstalledExtension) *ui.Element {
	if len(installed) == 0 {
		return ui.Section("Installed",
			ui.EmptyState(emptyIconCollections, "No extensions installed"))
	}
	rows := make([]ui.El, 0, len(installed))
	for _, e := range installed {
		rows = append(rows, ui.Stack("horizontal", 8,
			ui.Subtitle(e.ModuleID+" · "+e.Version),
			ui.Button("Uninstall", "ghost", ui.OnTap(ui.Invoke(uninstallExtensionAction, map[string]any{
				paramModuleID: e.ModuleID,
			}))),
		))
	}
	return ui.Section("Installed", ui.Stack("vertical", 8, rows...))
}

// availableExtensionsSection lists what the repository offers that is not already
// installed, each with a control to install it. One already installed is not
// repeated here — it is in the section above with its Uninstall control.
func availableExtensionsSection(available []app.ExtensionCatalogueEntry, installed map[string]bool) *ui.Element {
	rows := make([]ui.El, 0, len(available))
	for _, e := range available {
		if installed[e.ModuleID] {
			continue
		}
		rows = append(rows, ui.Stack("horizontal", 8,
			ui.Subtitle(e.Name+" · "+e.Version),
			ui.Button("Install", "secondary", ui.OnTap(ui.Invoke(installExtensionAction, map[string]any{
				"repository":  e.Repository,
				paramModuleID: e.ModuleID,
			}))),
		))
	}
	if len(rows) == 0 {
		return ui.Section("Available",
			ui.EmptyState(emptyIconCollections, "Everything the repository offers is installed"))
	}
	return ui.Section("Available", ui.Stack("vertical", 8, rows...))
}

// backToSettingsIndex is the way out of a module's screen. It is the Platform's
// control on the Platform's frame: a module cannot render it, because a module
// does not know it is being hosted inside a settings host.
func backToSettingsIndex() *ui.Element {
	return ui.Button("← Settings", "ghost", ui.OnTap(ui.Navigate(screenSettings, nil)))
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
