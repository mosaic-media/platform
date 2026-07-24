// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings section keys. A key names the panel a nav item opens and is what the
// nav highlights against, so the two sides cannot drift.
//
// Module keys are namespaced because a module id is user-supplied vocabulary: a
// module called "extensions" must not light up the Extensions nav item.
const (
	sectionExtensions = "extensions"
	moduleSectionKey  = "module:"
)

// settingsNavEntry is one row of the Platform's settings nav.
type settingsNavEntry struct {
	// key identifies the section this row opens — matched against the open one
	// to decide which row is highlighted.
	key   string
	label string
	icon  string
	// action is what tapping the row emits. Most navigate to a section of the
	// settings screen; extensions navigates to its own screen (ADR 0081).
	action ui.Action
	// panel is false for a row that leaves the settings screen entirely. Such a
	// row is never auto-selected — there is no panel body to render for it here.
	panel bool
}

// settingsNavGroup is one labelled run of nav rows ("Server", "Modules").
type settingsNavGroup struct {
	label   string
	entries []settingsNavEntry
}

// settingsNavModel is the whole left column: the sections it offers, and the
// level control at its foot.
//
// Expert mode is a control on the nav rather than a section in it — Kodi's
// settings level, which decides how much of the nav exists rather than being
// somewhere you go. It used to be a section whose entire content was a button
// that changed what the nav showed, which is a section about the nav.
type settingsNavModel struct {
	groups []settingsNavGroup
	// showExpertMode is whether the control is drawn at all — a caller who
	// could not use what it reveals is not shown it (ADR 0058).
	showExpertMode bool
	expertModeOn   bool
	// selected is whether a section was asked for rather than defaulted to. The
	// frame renders as a list-then-drill-down on a phone and as two panes on a
	// desktop, from one payload, and this is what tells those apart.
	selected bool
}

// settingsScreen is the settings surface: a Platform-owned nav beside a panel
// that carries the open section (ADR 0038).
//
// The nav is the Platform's on every render, including a module's section — a
// module fills the panel and cannot draw the nav, because a module does not know
// it is being hosted. That is also what replaced the old "← Settings" button:
// the way back out used to be a control the host appended above the module's
// tree, and it is now the nav that never left the screen.
//
// Which sections exist is decided per caller, so a settings screen is never a
// list of things the person reading it cannot open.
func (s *Service) settingsScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	nav, err := s.settingsNav(ctx, caller)
	if err != nil {
		return nil, err
	}

	moduleID := stringParam(params, paramModuleID)
	section := stringParam(params, paramSection)
	active := openSection(nav.groups, moduleID, section)

	// Whether the caller ASKED for this section, which is not the same as whether
	// a panel is being rendered: opening settings with no params still resolves a
	// default section, because a desktop wants one rather than an empty pane. A
	// phone renders the nav as a list and drills into a section, so it needs the
	// difference — without it, settings on a phone would open inside whichever
	// module sorted first, offering a way back to a list nobody had seen.
	nav.selected = moduleID != "" || section != ""

	if strings.HasPrefix(active, moduleSectionKey) {
		return s.moduleSettingsPanel(ctx, caller, nav, strings.TrimPrefix(active, moduleSectionKey))
	}
	return settingsFrame(nav, active, "", noSectionPanel(nav.groups)), nil
}

// settingsNav builds the nav for this caller: the install-level sections they
// can use, then a row per module that contributes a settings screen.
//
// The modules group is the one ADR 0038 exists for. It used to be a list on an
// index screen, which meant a module's settings were two navigations deep and
// the way back was a button the host drew; as nav rows they are one tap from
// each other and from everything else here.
func (s *Service) settingsNav(ctx context.Context, caller v1.Caller) (settingsNavModel, error) {
	nav := settingsNavModel{}
	var groups []settingsNavGroup

	// Server — the install itself. Extensions keeps its own screen (ADR 0081):
	// listing what the repository offers is a network read, and it stays
	// something a user opens rather than something every settings render does.
	// It renders inside this same frame, so the nav persists across it.
	if s.content.CallerCan(ctx, caller, app.ActionModuleRead, "extension") {
		groups = append(groups, settingsNavGroup{label: "Server", entries: []settingsNavEntry{{
			key:    sectionExtensions,
			label:  "Extensions",
			icon:   "grid",
			action: ui.Navigate(screenExtensions, nil),
		}}})
	}

	res, err := s.content.ListSettingsModules(ctx, app.ListSettingsModulesQuery{Caller: caller})
	if err != nil {
		return settingsNavModel{}, err
	}
	if len(res.Modules) > 0 {
		entries := make([]settingsNavEntry, 0, len(res.Modules))
		for _, m := range res.Modules {
			entries = append(entries, settingsNavEntry{
				key:    moduleSectionKey + m.ModuleID,
				label:  m.Name,
				icon:   "settings",
				action: ui.Navigate(screenSettings, map[string]any{paramModuleID: m.ModuleID}),
				panel:  true,
			})
		}
		groups = append(groups, settingsNavGroup{label: "Modules", entries: entries})
	}

	// The expert-mode level, and what it reveals: the diagnostics screens appear
	// as their own group only while it is on (ADR 0058).
	//
	// The control is drawn only for a caller who could use what it reveals — a
	// normal user does not see the switch at all, rather than seeing it and being
	// denied the data behind it. It remains a hint and never a gate: the three
	// screens each authorise telemetry.read for themselves, so navigating
	// straight to one without the grant is refused regardless of what was drawn.
	nav.showExpertMode = s.content.CallerCan(ctx, caller, app.ActionTelemetryRead, "telemetry")
	if nav.showExpertMode {
		nav.expertModeOn = s.content.ExpertModeEnabled(ctx, caller)
	}
	if nav.showExpertMode && nav.expertModeOn {
		groups = append(groups, settingsNavGroup{label: "Diagnostics", entries: []settingsNavEntry{
			{key: "logs", label: "Logs", icon: "list", action: ui.Navigate(screenLogs, nil)},
			{key: "traces", label: "Traces", icon: "info", action: ui.Navigate(screenTraces, nil)},
		}})
	}

	nav.groups = groups
	return nav, nil
}

// openSection resolves which section the panel shows: the requested module, the
// requested section, or — when the screen is opened with no params, as it is
// from the app nav — the first section that has a panel to render.
func openSection(groups []settingsNavGroup, moduleID, section string) string {
	switch {
	case moduleID != "":
		return moduleSectionKey + moduleID
	case section != "":
		return section
	}
	for _, g := range groups {
		for _, e := range g.entries {
			if e.panel {
				return e.key
			}
		}
	}
	return ""
}

// moduleSettingsPanel hosts a module's own contributed settings UI (ADR 0038).
// The Platform owns the frame; the module fills the panel — validated by the app
// service, decoded here, and rendered as it was returned.
func (s *Service) moduleSettingsPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel, moduleID string) (sdui.Node, error) {
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

	active := moduleSectionKey + moduleID
	heading, body := modulePanel(node)
	if heading == "" {
		heading = navLabel(nav.groups, active)
	}
	return settingsFrame(nav, active, heading, body...), nil
}

// modulePanel adapts a module's contributed tree to the panel it now fills, and
// returns the heading to title that panel with.
//
// A module roots its settings UI at a Screen, which was right when its settings
// WERE a whole screen: the Screen definition carries the page gutter and the top
// padding that clears the floating nav. Rendered inside the panel those apply a
// second time, so the module's content would sit indented and pushed down from
// the Platform's own sections on the same frame.
//
// So the Screen's own container is dropped and its title becomes the panel
// heading. **What the module contributed is untouched** — every child renders in
// the order it was returned, and any other root is hosted verbatim. The Platform
// is replacing its own outer container, which is the half of the tree ADR 0038
// already says belongs to the host.
func modulePanel(node sdui.Node) (string, []sdui.Node) {
	if node.GetType() != sdui.TypeScreen {
		return "", []sdui.Node{node}
	}
	title, _ := node.GetProps().AsMap()["title"].(string)
	return title, node.GetChildren()
}

// navLabel is what the nav calls a section — the fallback heading for a module
// that titled its screen with nothing.
func navLabel(groups []settingsNavGroup, key string) string {
	for _, g := range groups {
		for _, e := range g.entries {
			if e.key == key {
				return e.label
			}
		}
	}
	return ""
}

// settingsFrame composes the Platform's settings chrome around a panel body: the
// nav, with the open section marked, and the heading and body beside it.
//
// It is composed as protobuf rather than through the ui builder because one of
// its bodies — a module's settings UI — is an already-built node, and ui.El's
// method is unexported, so a node someone else produced cannot be placed back
// into a builder tree from this package. Appending children directly is the
// honest way to combine the two, and doing it on every path keeps one frame
// rather than two that must be kept looking alike.
func settingsFrame(nav settingsNavModel, active, heading string, body ...sdui.Node) sdui.Node {
	frame := ui.SettingsFrame("Settings",
		ui.Heading(heading),
		ui.Selected(nav.selected),
		settingsNavSlot(nav.groups, active),
		expertModeFooter(nav)).Build()
	frame.Children = append(frame.Children, body...)
	return frame
}

// expertModeFooter is the level control at the foot of the nav: a switch that
// governs how much of the nav exists (ADR 0058), not a place to navigate to.
//
// The switch carries the value it is moving TO, because the Switch primitive
// emits the action it was given and does not author one — the server decides
// what a flip means, the client reports that it happened. The rule it divides
// itself off with is part of what this emits, so a caller who may not see the
// control gets an empty footer and no stray divider.
func expertModeFooter(nav settingsNavModel) ui.El {
	if !nav.showExpertMode {
		return ui.Slot("footer")
	}
	return ui.Slot("footer",
		ui.Divider(),
		ui.Toggle("Expert mode",
			ui.On(nav.expertModeOn),
			ui.OnTap(ui.Invoke(setPreferenceMutation, map[string]any{
				"key":   domain.PreferenceExpertMode,
				"value": !nav.expertModeOn,
			}))))
}

// settingsNavSlot renders the nav rows into the frame's nav slot, highlighting
// the open one. The Platform decides which row is active because it is the side
// that knows the params (ADR 0039) — the client has nothing to compare, since
// every section here is the same screen.
func settingsNavSlot(groups []settingsNavGroup, active string) ui.El {
	els := make([]ui.El, 0, len(groups))
	for _, g := range groups {
		rows := make([]ui.El, 0, len(g.entries))
		for _, e := range g.entries {
			rows = append(rows, ui.SettingsNavItem(e.label, e.icon,
				ui.Active(e.key == active),
				ui.OnTap(e.action)))
		}
		els = append(els, ui.SettingsNavGroup(g.label, ui.Group(rows...)))
	}
	return ui.Slot("nav", els...)
}

// noSectionPanel is the panel when nothing is open. Both of its states are
// reachable and they are not the same thing: a build composed with no settings-UI
// module and a caller with no install-level permission has nothing to configure
// at all, while a caller whose only row leaves this screen has a nav to use.
func noSectionPanel(groups []settingsNavGroup) sdui.Node {
	if len(groups) == 0 {
		return ui.Section("Settings",
			ui.EmptyState(emptyIconCollections, "Nothing in this build contributes settings")).Build()
	}
	return ui.Section("Settings",
		ui.EmptyState(emptyIconCollections, "Choose a section")).Build()
}

// extensionsScreen is the browse-and-install surface for extension modules
// (ADR 0081): what a user has installed, with a way to remove each, and what the
// trusted repository offers, with a way to install each. It is its own screen
// because listing what is available reaches the repository over the network, so
// it happens when a user opens this rather than on every settings render — and
// it renders inside the settings frame, so opening it does not cost the nav.
func (s *Service) extensionsScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	nav, err := s.settingsNav(ctx, caller)
	if err != nil {
		return nil, err
	}
	// Reached by tapping its nav row, so it is always a section a caller asked
	// for — on a phone this screen IS the drill-down, not a pane beside a list.
	nav.selected = true
	installed, err := s.content.ListInstalledExtensions(ctx, app.ListInstalledExtensionsQuery{Caller: caller})
	if err != nil {
		return nil, err
	}
	installedByID := make(map[string]bool, len(installed))
	for _, e := range installed {
		installedByID[e.ModuleID] = true
	}

	// Available is a network read. If the repository is unreachable, show what is
	// installed and say the catalogue could not be loaded rather than failing the
	// whole screen — a user must still be able to uninstall when the repo is down.
	available, availErr := s.content.ListAvailableExtensions(ctx, app.ListAvailableExtensionsQuery{Caller: caller})

	// An installed module's roles are known to the catalogue, not to the install
	// record — so an installed card describes what it does when the repository
	// answers, and carries its identity and provenance regardless.
	providesByID := make(map[string][]string, len(available))
	describedByID := make(map[string]string, len(available))
	for _, e := range available {
		providesByID[e.ModuleID] = e.Provides
		describedByID[e.ModuleID] = e.Description
	}

	body := []sdui.Node{installedExtensionsSection(installed, providesByID, describedByID).Build()}
	if availErr != nil {
		body = append(body, ui.Section("Available",
			ui.EmptyState(emptyIconCollections, "The extension repository could not be reached")).Build())
	} else {
		body = append(body, availableExtensionsSection(available, installedByID).Build())
	}

	return settingsFrame(nav, sectionExtensions, "Extensions", body...), nil
}

// installOverlay is the install confirmation for one offered extension: what it
// is, what it would be able to do once it runs, where its bytes come from — and
// the control that actually installs it (ADR 0081).
//
// It is an **overlay over the catalogue**, not a screen. Installing is running
// somebody else's signed binary on this machine, and the decision belongs to the
// list you are looking at: a screen would take the catalogue away, need a route
// and a way back, and turn "tell me about this one" into navigation. The
// authority is unchanged and lives in the command; what this adds is *informed*
// consent, which a row with an Install button on it cannot give.
//
// It carries the only installExtension action in the surface — the card opens
// this, so the act of installing always follows having been told what it does.
func installOverlay(e app.ExtensionCatalogueEntry) *ui.Element {
	return ui.Component("Box",
		ui.Prop("style", map[string]any{"direction": "column", "gap": 5, "p": 6, "maxWidth": 520}),
		ui.Component("Text", ui.Prop("text", e.Name),
			ui.Prop("style", map[string]any{"variant": "2xl", "weight": "bold", "tracking": "tight"})),
		ui.Banner(extensionBlurb(e.Description, e.Provides)+" It runs as its own process, and you can remove it at any time.", ui.ToneInfo),
		capabilitiesSection(e.Provides),
		provenanceSection(e),
		ui.Stack("horizontal", 4,
			ui.Button("Install "+e.Name, "primary", ui.OnTap(ui.Invoke(installExtensionAction, map[string]any{
				"repository":  e.Repository,
				paramModuleID: e.ModuleID,
			}))),
			// Cancel dismisses rather than navigating: the overlay never went
			// anywhere, so there is nothing to go back to.
			ui.Button("Cancel", "ghost", ui.OnTap(ui.DismissOverlay()))))
}

// capabilitiesSection spells out each role the module declares, so consent is
// given to specific capabilities rather than to a word like "extension".
func capabilitiesSection(provides []string) *ui.Element {
	provides = shownCapabilities(provides)
	if len(provides) == 0 {
		return ui.Section("What it can do",
			ui.EmptyState(emptyIconCollections, "This module declares no capabilities"))
	}
	rows := make([]ui.El, 0, len(provides))
	for _, r := range provides {
		c := describeCapability(r)
		rows = append(rows, ui.Stack("horizontal", 4,
			ui.Badge(c.label, ui.ToneInfo),
			ui.Component("Text", ui.Prop("text", c.detail),
				ui.Prop("style", map[string]any{"variant": "sm", "color": "text-muted"}))))
	}
	return ui.Section("What it can do", ui.Stack("vertical", 4, rows...))
}

// provenanceSection is where the bytes come from. It is not decoration: the
// trust model (ADR 0065) is a signed index from a named repository, and the
// person consenting to run the binary is entitled to see which one.
func provenanceSection(e app.ExtensionCatalogueEntry) *ui.Element {
	return ui.Section("Where it comes from", ui.Stack("vertical", 2,
		ui.Component("Text", ui.Prop("text", "Repository: "+e.Repository),
			ui.Prop("style", map[string]any{"variant": "sm", "color": "text-muted"})),
		ui.Component("Text", ui.Prop("text", "Module: "+e.ModuleID+" · version "+e.Version),
			ui.Prop("style", map[string]any{"variant": "sm", "color": "text-muted"})),
		ui.Component("Text", ui.Prop("text", "Its signature and the binary's digest are verified before it is run."),
			ui.Prop("style", map[string]any{"variant": "sm", "color": "text-faint"}))))
}

// capability describes one provider role in the terms a person deciding about a
// module would use.
//
// The words are the **Platform's**, not the module's, and that is deliberate: a
// role means one thing here regardless of who fills it, so what a module can do
// is read off its signed manifest rather than off prose its author wrote about
// itself. A module cannot overstate what it will be able to do, because it is
// not the one saying it.
//
// What this is NOT is a description of what a particular extension is *for* —
// "AIOStreams aggregates many sources behind one instance" is the module's own
// sentence and nothing in the chain carries it: the SDK manifest has no
// description field, so neither does the extension manifest, the signed index,
// or the catalogue entry. Adding one is an additive SDK bump, a `host`
// pass-through, a line in each module and a re-release of each — recorded as a
// gap rather than faked here with a hand-maintained table of other people's
// modules.
type capability struct {
	role   v1.Role
	label  string
	detail string
}

// capabilities is the closed vocabulary of provider roles (ADR 0027) in plain
// words. A role with no entry still renders — under its own name, because an
// unknown capability must be visible rather than quietly dropped from the list
// somebody is about to consent to.
var capabilities = []capability{
	{v1.RoleMetadata, "Metadata", "Fills in titles, descriptions, cast and release details."},
	{v1.RoleSearch, "Search", "Answers searches for titles that are not in your library yet."},
	{v1.RoleCatalog, "Catalogs", "Contributes rows you can browse, like Popular or Trending."},
	{v1.RoleStream, "Streams", "Finds playable sources for a title."},
	{v1.RoleSubtitles, "Subtitles", "Supplies subtitle tracks for playback."},
	{v1.RoleArtwork, "Artwork", "Supplies posters, backdrops and logos."},
	{v1.RolePlayback, "Playback", "Resolves what actually plays, and where."},
}

// hiddenCapabilities are roles a user is never shown. settings_ui is plumbing:
// every module that needs configuring declares it, it says nothing about what
// the module is FOR, and listing it invites a user to weigh "adds a settings
// screen" against "finds streams" as if they were the same kind of fact.
var hiddenCapabilities = map[v1.Role]bool{v1.RoleSettingsUI: true}

// shownCapabilities drops the roles that are plumbing, preserving order.
func shownCapabilities(provides []string) []string {
	out := make([]string, 0, len(provides))
	for _, r := range provides {
		if !hiddenCapabilities[v1.Role(r)] {
			out = append(out, r)
		}
	}
	return out
}

// describeCapability returns the vocabulary entry for a declared role, falling
// back to the raw role name so an unrecognised one is still shown.
func describeCapability(role string) capability {
	for _, c := range capabilities {
		if string(c.role) == role {
			return c
		}
	}
	return capability{role: v1.Role(role), label: role, detail: "A capability this build of Mosaic does not have a description for."}
}

// capabilityProps renders declared roles as the chips an ExtensionCard shows.
func capabilityProps(provides []string) []any {
	provides = shownCapabilities(provides)
	out := make([]any, 0, len(provides))
	for _, r := range provides {
		out = append(out, map[string]any{"label": describeCapability(r).label})
	}
	return out
}

// extensionBlurb is what a card and a confirmation say about a module: its own
// sentence when it publishes one, and otherwise what its capabilities amount to.
//
// The module's words come first because they answer a question the Platform
// cannot — what this thing IS, as opposed to which roles it fills. Both are from
// the same signed manifest, so neither is prose anybody added downstream.
func extensionBlurb(description string, provides []string) string {
	if strings.TrimSpace(description) != "" {
		return strings.TrimSpace(description)
	}
	return extensionSummary(provides)
}

// extensionSummary is the fallback: what the module's signed manifest declares
// it fills, in a sentence.
func extensionSummary(provides []string) string {
	provides = shownCapabilities(provides)
	if len(provides) == 0 {
		return "Declares no capabilities, so it would add nothing on its own."
	}
	parts := make([]string, 0, len(provides))
	for _, r := range provides {
		parts = append(parts, strings.ToLower(describeCapability(r).label))
	}
	return "Adds " + joinWords(parts) + " to this install."
}

// joinWords renders a list as prose — "a, b and c" — because a settings screen
// is read, not parsed.
func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	default:
		return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
	}
}

// installedExtensionsSection lists each installed extension as a card carrying
// what it does and where its bytes came from. The empty state is the ordinary
// one for a fresh install (ADR 0081: nothing is installed by default).
func installedExtensionsSection(installed []app.InstalledExtension, provides map[string][]string, descriptions map[string]string) *ui.Element {
	if len(installed) == 0 {
		return ui.Section("Installed",
			ui.EmptyState(emptyIconCollections, "No extensions installed"))
	}
	cards := make([]ui.El, 0, len(installed))
	for _, e := range installed {
		// The catalogue is what knows a module's roles. When it is unreachable
		// the card still renders — with its identity and provenance and no
		// capability chips — because "the repository is down" must not make an
		// installed module unmanageable.
		roles := provides[e.ModuleID]
		cards = append(cards, ui.ExtensionCard(e.ModuleID,
			ui.Origin(e.Version+" · "+e.Repository+" · signed by "+e.SignedBy),
			ui.Summary(extensionBlurb(descriptions[e.ModuleID], roles)),
			ui.Capabilities(capabilityProps(roles)),
			ui.Button("Uninstall", "danger", ui.OnTap(ui.Invoke(uninstallExtensionAction, map[string]any{
				paramModuleID: e.ModuleID,
			})))))
	}
	return ui.Section("Installed", ui.Stack("vertical", 4, cards...))
}

// availableExtensionsSection cards what the repository offers that is not
// already installed. One already installed is not repeated here — it is in the
// section above with its Uninstall control.
//
// The control **opens a confirmation rather than installing**: running somebody
// else's binary is a decision, so it gets an overlay that says what the module
// would be able to do and where the bytes come from, and the install happens on
// the far side of it. A one-tap Install on a list is how you end up with a
// module you did not mean to run.
func availableExtensionsSection(available []app.ExtensionCatalogueEntry, installed map[string]bool) *ui.Element {
	cards := make([]ui.El, 0, len(available))
	for _, e := range available {
		if installed[e.ModuleID] {
			continue
		}
		cards = append(cards, ui.ExtensionCard(e.Name,
			ui.Origin(e.ModuleID+" · "+e.Version+" · "+e.Repository),
			ui.Summary(extensionBlurb(e.Description, e.Provides)),
			ui.Capabilities(capabilityProps(e.Provides)),
			ui.Button("Install…", "secondary", ui.OnTap(ui.Overlay(ui.SurfaceModal, installOverlay(e))))))
	}
	if len(cards) == 0 {
		return ui.Section("Available",
			ui.EmptyState(emptyIconCollections, "Everything the repository offers is installed"))
	}
	return ui.Section("Available", ui.Stack("vertical", 4, cards...))
}
