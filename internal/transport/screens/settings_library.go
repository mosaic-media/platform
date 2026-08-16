// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings › Library (platform#60) — where an administrator states what the
// library should contain, and reads what the last run did about it.
//
// Two things this panel is arranged around.
//
// A rule is created from a catalog you are looking at, not from a form with a
// module id in it. Typing a catalog id into a box is how you make a rule that
// silently matches nothing; picking the row the collections screen already shows
// cannot be misspelled.
//
// Nothing is created before its consequence is shown. Choosing a collection
// opens a confirmation that has evaluated the rule: how many titles it matches,
// how many the library already has, the first few by name, and what the first
// run will therefore add. platform#60 calls the first run the one most likely to
// surprise its author, and says bounding it and reporting it is part of the
// surface rather than a refinement.

// The Library section's params and actions.
const (
	// sectionLibraryRules is the settings section this panel fills.
	sectionLibraryRules = "library"
	// paramAddCatalog and paramAddModule open the confirmation for a proposed
	// collection rule.
	//
	// The module travels under its own key rather than under paramModuleID,
	// which is not a nicety: the settings screen reads paramModuleID as "open
	// this module's own settings panel" and resolves it before any section, so
	// a navigation that carried both would open the Stremio settings screen
	// instead of the rule confirmation. That is exactly the class of collision
	// the namespaced section keys already exist to prevent.
	paramAddCatalog = "addCatalog"
	paramAddModule  = "addModule"
	// paramRuleID names an existing rule for an action.
	paramRuleID = "ruleId"
	// paramBound is the proposed rule's bound, carried through the confirmation
	// so that changing it re-previews against the number that will apply.
	paramBound = "bound"

	createLibraryRuleAction     = "createLibraryRule"
	deleteLibraryRuleAction     = "deleteLibraryRule"
	setLibraryRuleEnabledAction = "setLibraryRuleEnabled"
	runLibraryMaintenanceAction = "runLibraryMaintenance"

	fieldRuleName = "ruleName"
)

// libraryRulesPanel is the Library section: the rules, what each last did, and
// the way to add another.
func (s *Service) libraryRulesPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel, params map[string]any) (sdui.Node, error) {
	if catalogID := stringParam(params, paramAddCatalog); catalogID != "" {
		return s.newLibraryRulePanel(ctx, caller, nav, params)
	}

	res, err := s.content.ListLibraryRules(ctx, app.ListLibraryRulesQuery{Caller: caller})
	if err != nil {
		return nil, err
	}

	canManage := s.content.CallerCan(ctx, caller, app.ActionLibraryRuleManage, "library_rule")

	body := []sdui.Node{}
	if len(res.Rules) == 0 {
		body = append(body, ui.EmptyState(emptyIconCollections,
			"No library rules yet",
			ui.Message("A rule says what this library should contain. Add one from a collection below "+
				"and the server keeps it filled, without anybody pressing Add."),
		).Build())
	} else {
		rows := make([]ui.El, 0, len(res.Rules))
		for _, listing := range res.Rules {
			rows = append(rows, s.libraryRuleRow(listing, canManage))
		}
		body = append(body, ui.Stack("vertical", 0, rows...).Build())

		if degraded := degradedRulesBanner(res.Rules); degraded != nil {
			body = append(body, degraded.Build())
		}
		if canManage {
			body = append(body, ui.Section("Keeping it current",
				ui.Banner("Rules are evaluated on a schedule. Running now does the same work early: "+
					"it adds what is missing, refreshes artwork and fills in anything with nothing to play. "+
					"It never removes a title.", ui.ToneInfo),
				ui.Button("Run maintenance now", "secondary", ui.IconName("refresh"),
					ui.OnTap(ui.Invoke(runLibraryMaintenanceAction, nil))),
			).Build())
		}
	}

	if canManage {
		add, err := s.addLibraryRuleSection(ctx, caller)
		if err != nil {
			return nil, err
		}
		body = append(body, add.Build())
	}

	return settingsFrame(nav, sectionLibraryRules, "Library",
		"What this server's library should contain, and what keeps it that way.",
		body...), nil
}

// libraryRuleRow is one rule: what it says, what it last did, and the two things
// that can be done to it.
//
// The controls sit in the row's own slot rather than as an action on the row.
// A SettingsRow reads no action prop: setting one compiles, renders, changes
// nothing and reports nothing.
func (s *Service) libraryRuleRow(listing app.LibraryRuleListing, canManage bool) ui.El {
	rule := listing.Rule

	els := []ui.El{
		ui.Summary(libraryRuleSummary(listing)),
		ui.Value(lastRunValue(rule.LastRun, s.now())),
	}
	if canManage {
		controls := []ui.El{}
		if rule.Enabled {
			controls = append(controls, ui.Button("Pause", "quiet",
				ui.OnTap(ui.Invoke(setLibraryRuleEnabledAction, map[string]any{
					paramRuleID: string(rule.ID), "enabled": false,
				}))))
		} else {
			controls = append(controls, ui.Button("Resume", "quiet",
				ui.OnTap(ui.Invoke(setLibraryRuleEnabledAction, map[string]any{
					paramRuleID: string(rule.ID), "enabled": true,
				}))))
		}
		controls = append(controls, ui.Button("Delete", "dangerQuiet",
			ui.OnTap(ui.Invoke(deleteLibraryRuleAction, map[string]any{
				paramRuleID: string(rule.ID),
			}))))
		els = append(els, ui.Group(controls...))
	}
	return ui.SettingsRow(rule.Name, els...)
}

// libraryRuleSummary is the line under a rule's name: what it is a rule over,
// its bound, and — when its module has gone — that it is degraded rather than
// working.
func libraryRuleSummary(listing app.LibraryRuleListing) string {
	rule := listing.Rule
	parts := []string{libraryRuleSubject(rule), fmt.Sprintf("first %d", rule.EffectiveBound())}
	if !listing.Available {
		// Degraded and visibly so, never deleted (platform#60). The row stays, the
		// statement stays, and the reason it is doing nothing is on the row
		// rather than left to be deduced from a run that adds nothing.
		parts = append(parts, "the "+rule.ModuleID+" module is not installed, so this rule is paused by circumstance")
	} else if !rule.Enabled {
		parts = append(parts, "paused")
	}
	return strings.Join(parts, " · ")
}

// libraryRuleSubject names what a rule is over, in the same words the panel that
// created it used.
func libraryRuleSubject(rule domain.LibraryRule) string {
	switch rule.Kind {
	case domain.LibraryRuleCollection:
		subject := rule.ModuleID + " · " + rule.CatalogID
		if rule.NativeType != "" {
			subject += " · " + rule.NativeType
		}
		return subject
	case domain.LibraryRuleQuery:
		return rule.ModuleID + " · search for “" + rule.Text + "”"
	default:
		return rule.ModuleID
	}
}

// lastRunValue is the right-hand column: the account of the last run, in the
// four numbers platform#60 asks every run to record.
//
// A rule that has never run says so. Rendering four zeroes for it would read as
// a rule that ran and found nothing, which is the opposite fact and the one an
// administrator would act on.
func lastRunValue(run domain.LibraryRuleRun, now time.Time) string {
	if run.NeverRun() {
		return "not run yet"
	}
	when := lastRunAgo(run.At, now)
	if run.Error != "" {
		return strings.TrimSpace(when + " · could not be evaluated")
	}
	counts := fmt.Sprintf("%d added · %d refreshed · %d skipped · %d failed",
		run.Created, run.Refreshed, run.Skipped, run.Failed)
	if when == "" {
		return counts
	}
	return when + " · " + counts
}

// degradedRulesBanner names every rule whose module has gone, once, above the
// list. The per-row summary already says it; this is what makes it noticeable
// on a screen with a dozen rows on it.
func degradedRulesBanner(rules []app.LibraryRuleListing) *ui.Element {
	var names []string
	for _, listing := range rules {
		if !listing.Available {
			names = append(names, listing.Rule.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return ui.Banner(
		joinWords(names)+" can no longer be evaluated, because the module each names is not installed. "+
			"The rules are kept and so is everything they added — reinstalling the module starts them again.",
		ui.ToneWarning)
}

// addLibraryRuleSection offers the collections the installed modules expose, as
// the thing a rule is made from.
//
// A rule over a catalog nobody can see is a rule nobody can check, so the offer
// is the same list the collections screen browses. A module that contributes no
// catalog contributes no rules, and says so rather than being absent.
func (s *Service) addLibraryRuleSection(ctx context.Context, caller v1.Caller) (*ui.Element, error) {
	res, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil {
		return nil, err
	}
	if len(res.Catalogs) == 0 {
		return ui.Section("Add a rule",
			ui.EmptyState(emptyIconCollections,
				"No collections to build a rule from",
				ui.Message("A rule follows a module's collection. Install or configure a module that "+
					"contributes one, and it appears here."))), nil
	}
	rows := make([]ui.El, 0, len(res.Catalogs))
	for _, c := range res.Catalogs {
		rows = append(rows, ui.SettingsRow(c.Catalog.Name,
			ui.Summary(c.ModuleID+" · "+c.Catalog.NativeType),
			ui.Group(ui.Button("Follow this…", "quiet",
				ui.OnTap(ui.Navigate(screenSettings, map[string]any{
					paramSection:    sectionLibraryRules,
					paramAddModule:  c.ModuleID,
					paramAddCatalog: c.Catalog.ID,
					paramNativeType: c.Catalog.NativeType,
					paramTitle:      c.Catalog.Name,
				}))))))
	}
	return ui.Section("Add a rule",
		ui.Banner("Following a collection keeps its titles in your library. Titles that leave the "+
			"collection stay — a rule adds and never removes.", ui.ToneInfo),
		ui.Stack("vertical", 0, rows...)), nil
}

// newLibraryRulePanel is the confirmation: what this rule would do, before it
// exists.
//
// It evaluates the rule to say so: a count of what a source actually offers, how
// much of it the library already has, and the first few titles by name, so a
// mistyped catalog is visible rather than discovered a run later.
func (s *Service) newLibraryRulePanel(ctx context.Context, caller v1.Caller, nav settingsNavModel, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, paramAddModule)
	catalogID := stringParam(params, paramAddCatalog)
	nativeType := stringParam(params, paramNativeType)
	name := stringParam(params, paramTitle)
	if name == "" {
		name = catalogID
	}
	bound := intParam(params, paramBound)
	if bound <= 0 {
		bound = domain.DefaultLibraryRuleBound
	}

	preview, previewErr := s.content.PreviewLibraryRule(ctx, app.PreviewLibraryRuleQuery{
		Caller: caller, Name: name, Kind: domain.LibraryRuleCollection,
		ModuleID: moduleID, CatalogID: catalogID, NativeType: nativeType, Bound: bound,
	})

	body := []sdui.Node{}
	if previewErr != nil {
		// A source that will not answer is a reason not to create the rule
		// blind. Saying so and withholding the control is platform#24's rule about
		// affordances with nothing behind them, applied to the one control whose
		// consequence nobody can see.
		body = append(body,
			ui.Banner("This collection could not be read just now, so there is no way to say what the "+
				"rule would do. Try again in a moment.", ui.ToneDanger).Build(),
			ui.Section("What went wrong",
				ui.Component("Text", ui.Prop("text", previewErr.Error()),
					ui.Prop("style", map[string]any{"variant": "sm", "color": "text-muted"}))).Build(),
			backToLibraryRules().Build())
		return settingsFrame(nav, sectionLibraryRules, "Follow "+name,
			"A rule over "+moduleID+" · "+catalogID+".", body...), nil
	}

	body = append(body, ui.Section("What the first run will do",
		ui.Banner(firstRunSentence(preview), ui.ToneInfo),
		ui.Grid(ui.MinColumnWidth(130), ui.Gap(3), ui.Group(
			ui.StatCard("Matched", strconv.Itoa(preview.Matched)),
			ui.StatCard("Already here", strconv.Itoa(preview.AlreadyInLibrary)),
			ui.StatCard("Will be added", strconv.Itoa(preview.WouldAdd)),
			ui.StatCard("Bound", strconv.Itoa(preview.Bound)),
		)),
	).Build())

	if len(preview.Sample) > 0 {
		chips := make([]ui.El, 0, len(preview.Sample))
		for _, title := range preview.Sample {
			chips = append(chips, ui.Badge(title, ui.ToneNeutral))
		}
		body = append(body, ui.Section("For example",
			ui.Stack("horizontal", 2, ui.Wrap(true), ui.Group(chips...))).Build())
	}

	// The bound, as navigations rather than as a form field. It changes what the
	// preview above says, and only the server can recompute that — a select the
	// client could change on its own would leave the numbers beside it
	// describing the bound that was chosen a moment ago. It is the same reason
	// the new-account form carries its preset in the route.
	body = append(body, ui.Section("How much of it",
		ui.Banner("A rule takes the first N of a collection, in the order the source ranks them. "+
			"Bounding it is what keeps a run's cost — and a source's rate limit — predictable.", ui.ToneInfo),
		ui.Stack("horizontal", 2, ui.Wrap(true), ui.Group(boundChoices(bound, params)...))).Build())

	body = append(body,
		ui.State(
			ui.Vars(sdui.Vars(
				sdui.Var(fieldRuleName, sdui.VarString, name),
				sdui.Var(fieldFormError, sdui.VarString, ""),
			)),
			ui.Form(
				ui.BindError(fieldFormError),
				ui.SubmitLabel("Follow this collection"),
				// Everything that addresses the catalog travels in the action
				// rather than in the form's scope: it is which form this is
				// rather than something the form collects, and contracts#19's merge
				// rule keeps the two apart.
				ui.SubmitAction(ui.Submit(ui.Invoke(createLibraryRuleAction, map[string]any{
					paramAddModule:  moduleID,
					paramAddCatalog: catalogID,
					paramNativeType: nativeType,
					paramBound:      bound,
				}), "")),
				ui.TextField("Name",
					ui.Name(fieldRuleName),
					ui.Placeholder(name),
					ui.Help("What you will call this rule. It is how the run log refers to it."),
					ui.Validators(map[string]any{"required": true})),
			),
		).Build(),
		backToLibraryRules().Build())

	return settingsFrame(nav, sectionLibraryRules, "Follow "+name,
		"A rule over "+moduleID+" · "+catalogID+", evaluated before it is created.", body...), nil
}

// firstRunSentence says plainly what pressing the button leads to.
func firstRunSentence(p app.PreviewLibraryRuleResult) string {
	switch {
	case p.Matched == 0:
		return "This collection is offering nothing at the moment, so the rule would add nothing yet. " +
			"It will add whatever appears later."
	case p.WouldAdd == 0:
		return fmt.Sprintf("All %d of these are already in your library, so the first run adds nothing — "+
			"it refreshes their artwork and fills in anything with nothing to play.", p.Matched)
	default:
		out := fmt.Sprintf("The first run will add %d %s to your library, and refresh the %d already here.",
			p.WouldAdd, plural(p.WouldAdd, "title"), p.AlreadyInLibrary)
		if p.Truncated {
			out += fmt.Sprintf(" The collection has more than %d; the rule takes the first %d.", p.Bound, p.Bound)
		}
		return out
	}
}

// boundChoices offers the handful of bounds worth choosing between, as
// navigations back into this panel so the preview above is recomputed for the
// one that is picked.
func boundChoices(current int, params map[string]any) []ui.El {
	offered := []int{20, 40, 100, 250, domain.MaxLibraryRuleBound}
	out := make([]ui.El, 0, len(offered))
	for _, n := range offered {
		next := map[string]any{}
		for k, v := range params {
			next[k] = v
		}
		next[paramBound] = n
		out = append(out, ui.Button(strconv.Itoa(n), chipStyle(n == current),
			ui.OnTap(ui.Navigate(screenSettings, next))))
	}
	return out
}

// backToLibraryRules is the way out of the confirmation without creating
// anything. The nav never left the screen, so this is not the only way back — it
// is the one that says "no" to the question the panel is asking.
func backToLibraryRules() *ui.Element {
	return ui.Button("Cancel", "ghost",
		ui.OnTap(ui.Navigate(screenSettings, map[string]any{paramSection: sectionLibraryRules})))
}

// lastRunAgo is how long ago a rule last ran, in the coarsest unit that is still
// true — the same phrasing the history screen uses, for the same reason: it is
// read to recognise, not to measure.
func lastRunAgo(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	switch elapsed := now.Sub(at); {
	case elapsed < 0:
		return ""
	case elapsed < time.Hour:
		return "just now"
	case elapsed < 24*time.Hour:
		return strconv.Itoa(int(elapsed.Hours())) + "h ago"
	default:
		return at.Format("2 January, 15:04")
	}
}
