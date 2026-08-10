// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"errors"
	"strings"
	"testing"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings › Library (platform#60, roadmap M2.2).
//
// The two properties worth guarding on this panel are the ones that would fail
// quietly: a rule whose module has gone must *say* it is degraded rather than
// looking like a rule that finds nothing, and the confirmation must state what
// the first run will do before offering the control that starts it.

func rulesService(rules ...app.LibraryRuleListing) *fakeQueries {
	fake := &fakeQueries{libraryRules: rules, allow: map[string]bool{}}
	for _, a := range app.AdministratorActions() {
		fake.allow[string(a)] = true
	}
	return fake
}

func collectionRule(name string, available bool, run domain.LibraryRuleRun) app.LibraryRuleListing {
	return app.LibraryRuleListing{
		Available: available,
		Rule: domain.LibraryRule{
			ID: domain.LibraryRuleID("rule-" + name), Name: name,
			Kind: domain.LibraryRuleCollection, ModuleID: "stremio",
			CatalogID: "top", NativeType: "movie", Bound: 40, Enabled: true,
			LastRun: run,
		},
	}
}

func TestTheLibrarySectionListsTheRulesAndWhatEachLastDid(t *testing.T) {
	ran := domain.LibraryRuleRun{
		At:      time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
		Matched: 40, Created: 12, Refreshed: 26, Skipped: 1, Failed: 1,
	}
	fake := rulesService(collectionRule("Top films", true, ran))
	svc := &Service{content: fake, clock: func() time.Time {
		return time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	}}

	node := render(t, svc, "settings", map[string]any{"section": "library"})
	text := treeStrings(node)

	if !strings.Contains(text, "Top films") {
		t.Errorf("the rule is not listed: %s", text)
	}
	// The four numbers platform#60 asks every run to record, where the
	// administrator managing the rules will read them — not only in a job log
	// behind expert mode.
	if !strings.Contains(text, "12 added · 26 refreshed · 1 skipped · 1 failed") {
		t.Errorf("the run's account is not on the rule: %s", text)
	}
}

// A rule that has never run must say so. Four zeroes would read as a rule that
// ran and found nothing, which is the opposite fact and the one somebody acts on.
func TestARuleThatHasNeverRunSaysSo(t *testing.T) {
	fake := rulesService(collectionRule("Top films", true, domain.LibraryRuleRun{}))
	node := render(t, &Service{content: fake}, "settings", map[string]any{"section": "library"})

	text := treeStrings(node)
	if !strings.Contains(text, "not run yet") {
		t.Errorf("a rule that has never run did not say so: %s", text)
	}
	if strings.Contains(text, "0 added") {
		t.Error("a rule that has never run rendered as a run that found nothing")
	}
}

// A rule survives its module being uninstalled: degraded and *visibly* so
// (platform#60). Without this it looks exactly like a rule whose catalog is empty.
func TestARuleWhoseModuleIsGoneReadsAsDegraded(t *testing.T) {
	fake := rulesService(collectionRule("Top films", false, domain.LibraryRuleRun{}))
	node := render(t, &Service{content: fake}, "settings", map[string]any{"section": "library"})

	text := treeStrings(node)
	if !strings.Contains(text, "not installed") {
		t.Errorf("a degraded rule reads as a healthy one: %s", text)
	}
	// And it is still there, which is the half a deletion would have got wrong.
	if !strings.Contains(text, "Top films") {
		t.Error("the degraded rule was dropped from the list")
	}
}

// Every affordance on this panel is administrator authority. Somebody who may
// read the rules and not change them is shown the rules and no controls, rather
// than controls that will be refused (platform#24).
func TestAViewerIsOfferedNoRuleControls(t *testing.T) {
	fake := rulesService(collectionRule("Top films", true, domain.LibraryRuleRun{}))
	fake.allow[string(app.ActionLibraryRuleManage)] = false

	node := render(t, &Service{content: fake}, "settings", map[string]any{"section": "library"})
	if _, ok := findButton(node, "Delete"); ok {
		t.Error("somebody who cannot manage rules is offered Delete")
	}
	if _, ok := findButton(node, "Run maintenance now"); ok {
		t.Error("somebody who cannot manage rules is offered to run the pass")
	}
	if !strings.Contains(treeStrings(node), "Top films") {
		t.Error("the rules themselves were hidden, not only the controls")
	}
}

// The nav row is what makes the section reachable, and it is gated on its own
// permission rather than on the People one beside it.
func TestTheSettingsNavOffersLibraryOnlyToSomebodyWhoMayReadRules(t *testing.T) {
	fake := rulesService()
	node := render(t, &Service{content: fake}, "settings", nil)
	if _, ok := findNavItem(node, "Library"); !ok {
		t.Error("an administrator is offered no way into the library rules")
	}

	fake.allow[string(app.ActionLibraryRuleRead)] = false
	node = render(t, &Service{content: fake}, "settings", nil)
	if _, ok := findNavItem(node, "Library"); ok {
		t.Error("somebody who may not read rules is shown the section anyway")
	}
}

// The trap platform#60 names: the first run of a new rule is the one most likely to
// surprise its author. The confirmation must have evaluated it.
func TestFollowingACollectionSaysWhatTheFirstRunWillDo(t *testing.T) {
	fake := rulesService()
	fake.catalogs = []app.ModuleCatalog{
		{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Top films"}},
	}
	fake.preview = app.PreviewLibraryRuleResult{
		Matched: 40, AlreadyInLibrary: 12, WouldAdd: 28, Bound: 40, Truncated: true,
		Sample: []string{"Arrival", "Dune"},
	}
	svc := &Service{content: fake}

	node := render(t, svc, "settings", map[string]any{
		"section": "library", "addModule": "stremio", "addCatalog": "top",
		"nativeType": "movie", "title": "Top films",
	})
	text := treeStrings(node)

	if !strings.Contains(text, "add 28 titles") {
		t.Errorf("the confirmation did not say what the first run adds: %s", text)
	}
	if !strings.Contains(text, "Arrival") {
		t.Error("the confirmation named no titles, so a mistyped catalog is invisible")
	}
	if !strings.Contains(text, "takes the first 40") {
		t.Errorf("the confirmation did not say the collection is larger than the bound: %s", text)
	}
	if _, ok := findButton(node, "Cancel"); !ok {
		t.Error("the confirmation offers no way to say no")
	}

	// And the control that creates it carries the catalog in the action, so the
	// form collects only the name. A rule addressed by a typed-in catalog id is
	// a rule that silently matches nothing.
	form, ok := find(node, "Form")
	if !ok {
		t.Fatal("the confirmation has no form to create the rule from")
	}
	// A Submit wraps the Invoke it will send (contracts#19), and an Invoke carries
	// its arguments as `input` rather than as `params` — the same distinction
	// that makes a navigate and a mutation different things on the wire.
	submit, _ := prop(form, "submitAction").(map[string]any)
	nested, _ := submit["actions"].([]any)
	if len(nested) != 1 {
		t.Fatalf("the form's submit wraps %d actions, want the one that creates the rule", len(nested))
	}
	invoke, _ := nested[0].(map[string]any)
	if invoke["mutation"] != "createLibraryRule" {
		t.Fatalf("the form submits %v, want createLibraryRule", invoke["mutation"])
	}
	input, _ := invoke["input"].(map[string]any)
	if input["addCatalog"] != "top" || input["addModule"] != "stremio" {
		t.Fatalf("the create action carries %v, want the catalog it is following", input)
	}
}

// A source that will not answer cannot be previewed, so the control that would
// create the rule blind is withheld and the reason is stated.
func TestFollowingAnUnreachableCollectionOffersNothing(t *testing.T) {
	fake := rulesService()
	fake.previewErr = errors.New("the addon did not answer")
	node := render(t, &Service{content: fake}, "settings", map[string]any{
		"section": "library", "addModule": "stremio", "addCatalog": "top",
		"nativeType": "movie", "title": "Top films",
	})

	if _, ok := find(node, "Form"); ok {
		t.Error("a rule can be created without anybody being told what it will do")
	}
	if !strings.Contains(treeStrings(node), "could not be read") {
		t.Errorf("the panel did not say why it is offering nothing: %s", treeStrings(node))
	}
}

// The way in has to exist: a rule is made from a catalog you are looking at.
func TestTheLibrarySectionOffersTheCollectionsToFollow(t *testing.T) {
	fake := rulesService()
	fake.catalogs = []app.ModuleCatalog{
		{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Top films"}},
	}
	node := render(t, &Service{content: fake}, "settings", map[string]any{"section": "library"})

	var buttons []sdui.Node
	findAll(node, "Button", &buttons)
	var opens bool
	for _, b := range buttons {
		params, _ := actionOf(b)["params"].(map[string]any)
		if params["addCatalog"] == "top" {
			opens = true
		}
	}
	if !opens {
		t.Errorf("no collection can be followed: %s", treeStrings(node))
	}
}
