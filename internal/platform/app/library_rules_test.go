// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Library rules and the maintenance pass (platform#60, roadmap M2.2–2.3).
//
// The properties under test are the ones the record is emphatic about and that
// nothing else would catch: a second run adds no duplicates, a rule survives its
// module being uninstalled, a run says what it did, and the pass acts as the
// install rather than as whoever triggered it.

// catalogModule is a fake module that fills the catalog and metadata roles and
// materialises through the ContentService it is handed, deduping on the external
// id exactly as every real module does.
//
// The dedup is the important part of the fake. Every module in the fleet looks
// the title up before writing and reports AlreadyKnown when it finds it, and a
// fake that always created would prove idempotency the code does not have.
type catalogModule struct {
	id string
	// titles is what the catalog offers, in order.
	titles []string
	// pageSize splits titles into pages, so the paging the collection rule does
	// is exercised rather than assumed. Zero serves them all at once.
	pageSize int
	// failTitle makes materialising one title fail, for the best-effort case.
	failTitle string
	// imported records every native id Import was asked for, in order.
	imported []string
	// callers records who each import acted as, which is how the system
	// principal is asserted at the seam that matters.
	callers []string
}

func (m *catalogModule) Manifest() v1.Manifest {
	return v1.Manifest{
		ID: m.id, Version: "0.0.1", Name: "Catalog module",
		Provides: []v1.Role{v1.RoleCatalog, v1.RoleMetadata, v1.RoleSearch},
	}
}

func (m *catalogModule) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	m.imported = append(m.imported, req.Ref.NativeID)
	m.callers = append(m.callers, req.Caller.Session)
	if req.Ref.NativeID == m.failTitle {
		return v1.ImportResult{}, contracts.NewError(contracts.Unavailable, "the source would not answer for "+req.Ref.NativeID)
	}

	// Dedup before writing, under the shared external id — what every real
	// module does, and what makes a re-import the idempotent no-op platform#18
	// promises.
	found, err := svc.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: req.Caller, Scheme: "imdb", Value: req.Ref.ExternalID,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	for _, node := range found.Nodes {
		if node.IsRoot() {
			return v1.ImportResult{WorkID: node.ID, AlreadyKnown: true}, nil
		}
	}

	ids, _ := json.Marshal(map[string]string{"imdb": req.Ref.ExternalID})
	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: req.Caller, MediaType: v1.MediaMovie, Title: req.Ref.NativeID, ExternalIDs: ids,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	return v1.ImportResult{WorkID: work.Work.ID}, nil
}

func (m *catalogModule) Catalogs(_ context.Context, _ v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	return v1.CatalogsResponse{Catalogs: []v1.Catalog{
		{ID: "top", NativeType: "movie", Name: "Top films"},
	}}, nil
}

func (m *catalogModule) CatalogItems(_ context.Context, req v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	page := m.pageSize
	if page <= 0 {
		page = len(m.titles)
	}
	if req.Skip >= len(m.titles) {
		return v1.CatalogItemsResponse{}, nil
	}
	rest := m.titles[req.Skip:]
	hasMore := len(rest) > page
	if hasMore {
		rest = rest[:page]
	}
	items := make([]v1.CatalogItem, 0, len(rest))
	for _, title := range rest {
		items = append(items, v1.CatalogItem{Ref: catalogRef(m.id, title), Title: title})
	}
	return v1.CatalogItemsResponse{Items: items, HasMore: hasMore}, nil
}

func (m *catalogModule) Search(_ context.Context, req v1.SearchRequest) (v1.SearchResponse, error) {
	out := make([]v1.SearchResult, 0, len(m.titles))
	for _, title := range m.titles {
		if req.Text != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(req.Text)) {
			continue
		}
		out = append(out, v1.SearchResult{Ref: catalogRef(m.id, title), Title: title})
	}
	return v1.SearchResponse{Results: out}, nil
}

func (m *catalogModule) Metadata(_ context.Context, req v1.MetadataRequest) (v1.ContentMetadata, error) {
	return v1.ContentMetadata{Ref: req.Ref, Title: req.Ref.NativeID}, nil
}

func catalogRef(provider, title string) v1.ContentRef {
	return v1.ContentRef{
		Provider: provider, NativeID: title, NativeType: "movie", MediaType: v1.MediaMovie,
		ExternalScheme: "imdb", ExternalID: "tt-" + title,
	}
}

func rulesFixture(t *testing.T, mods ...*catalogModule) (*app.Service, *fakeDB, domain.SessionID) {
	svc, db, _, session := rulesFixtureWithRegistry(t, mods...)
	return svc, db, session
}

// rulesFixtureWithRegistry hands back the registry as well, for the one test
// that needs to uninstall a module mid-flight.
func rulesFixtureWithRegistry(t *testing.T, mods ...*catalogModule) (*app.Service, *fakeDB, *app.CapabilityRegistry, domain.SessionID) {
	t.Helper()
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	db := newFakeDB()
	registry := app.NewCapabilityRegistry()
	for _, m := range mods {
		registry.Register(m)
	}
	svc := newTestServiceWithCapabilities(db, &trace{}, now, registry)
	db.seedUser(domain.User{ID: "u-1", Username: "curator", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())
	return svc, db, registry, "s-1"
}

// requireCategory asserts an error's Platform category, so a test says which
// refusal it expected rather than only that something failed.
func requireCategory(t *testing.T, err error, want contracts.ErrorCategory) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s error, got nil", want)
	}
	if got := contracts.CategoryOf(err); got != want {
		t.Fatalf("CategoryOf(err) = %s, want %s (err = %v)", got, want, err)
	}
}

func createCollectionRule(t *testing.T, svc *app.Service, session domain.SessionID, name, moduleID string, bound int) domain.LibraryRule {
	t.Helper()
	res, err := svc.CreateLibraryRule(context.Background(), app.CreateLibraryRuleCommand{
		Caller: v1.Caller{Session: string(session)}, Name: name,
		Kind: domain.LibraryRuleCollection, ModuleID: moduleID,
		CatalogID: "top", NativeType: "movie", Bound: bound,
	})
	if err != nil {
		t.Fatalf("CreateLibraryRule(%s): %v", name, err)
	}
	return res.Rule
}

func TestCreateLibraryRule(t *testing.T) {
	ctx := context.Background()

	t.Run("stores the statement and materialises nothing", func(t *testing.T) {
		mod := &catalogModule{id: "stremio", titles: []string{"Arrival", "Dune"}}
		svc, db, session := rulesFixture(t, mod)

		rule := createCollectionRule(t, svc, session, "Top films", "stremio", 0)
		if rule.ID == "" || !rule.Enabled {
			t.Fatalf("stored rule = %+v, want an id and enabled", rule)
		}
		if rule.CreatedBy != "u-1" {
			t.Fatalf("CreatedBy = %q, want the administrator who wrote it", rule.CreatedBy)
		}
		// Saving states an intention; a run acts on it. Materialising on save is
		// the surprise platform#60 names, and this is the assertion that keeps it
		// from creeping back in.
		if len(mod.imported) != 0 {
			t.Fatalf("creating a rule imported %v, want nothing until a run", mod.imported)
		}
		db.mu.Lock()
		nodes := len(db.nodes)
		db.mu.Unlock()
		if nodes != 0 {
			t.Fatalf("creating a rule wrote %d nodes", nodes)
		}
	})

	t.Run("a rule with no catalog is refused", func(t *testing.T) {
		svc, _, session := rulesFixture(t, &catalogModule{id: "stremio"})
		_, err := svc.CreateLibraryRule(ctx, app.CreateLibraryRuleCommand{
			Caller: v1.Caller{Session: string(session)}, Name: "Nothing",
			Kind: domain.LibraryRuleCollection, ModuleID: "stremio",
		})
		requireCategory(t, err, contracts.InvalidArgument)
	})

	// The two kinds are closed (platform#60): a rule over the library's own
	// contents is a view, not a source.
	t.Run("a third kind is refused", func(t *testing.T) {
		svc, _, session := rulesFixture(t, &catalogModule{id: "stremio"})
		_, err := svc.CreateLibraryRule(ctx, app.CreateLibraryRuleCommand{
			Caller: v1.Caller{Session: string(session)}, Name: "Everything I own",
			Kind: "library", ModuleID: "stremio",
		})
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("a bound beyond the ceiling is refused", func(t *testing.T) {
		svc, _, session := rulesFixture(t, &catalogModule{id: "stremio"})
		_, err := svc.CreateLibraryRule(ctx, app.CreateLibraryRuleCommand{
			Caller: v1.Caller{Session: string(session)}, Name: "Everything",
			Kind: domain.LibraryRuleCollection, ModuleID: "stremio",
			CatalogID: "top", NativeType: "movie", Bound: 10_000,
		})
		requireCategory(t, err, contracts.InvalidArgument)
	})
}

// The preview is what makes the first run not a surprise. It must count what the
// source offers, how much of it is already owned, and stop at the bound.
func TestPreviewLibraryRule(t *testing.T) {
	ctx := context.Background()
	mod := &catalogModule{id: "stremio", titles: []string{"Arrival", "Dune", "Contact", "Solaris"}, pageSize: 2}
	svc, _, session := rulesFixture(t, mod)

	t.Run("says what a proposed rule would add, before it exists", func(t *testing.T) {
		res, err := svc.PreviewLibraryRule(ctx, app.PreviewLibraryRuleQuery{
			Caller: v1.Caller{Session: string(session)}, Name: "Top films",
			Kind: domain.LibraryRuleCollection, ModuleID: "stremio",
			CatalogID: "top", NativeType: "movie",
		})
		if err != nil {
			t.Fatalf("PreviewLibraryRule: %v", err)
		}
		if res.Matched != 4 || res.WouldAdd != 4 || res.AlreadyInLibrary != 0 {
			t.Fatalf("preview = %+v, want four matched and four to add", res)
		}
		if len(res.Sample) == 0 {
			t.Fatal("the preview named no titles, so a mistyped catalog is invisible")
		}
	})

	t.Run("the bound is applied and reported as truncation", func(t *testing.T) {
		res, err := svc.PreviewLibraryRule(ctx, app.PreviewLibraryRuleQuery{
			Caller: v1.Caller{Session: string(session)}, Name: "Top films",
			Kind: domain.LibraryRuleCollection, ModuleID: "stremio",
			CatalogID: "top", NativeType: "movie", Bound: 3,
		})
		if err != nil {
			t.Fatalf("PreviewLibraryRule: %v", err)
		}
		if res.Matched != 3 || res.Bound != 3 {
			t.Fatalf("preview = %+v, want the bound applied", res)
		}
		if !res.Truncated {
			t.Fatal("the collection had more to give and the preview did not say so")
		}
	})

	t.Run("previewing a rule whose module is gone is NotFound", func(t *testing.T) {
		_, err := svc.PreviewLibraryRule(ctx, app.PreviewLibraryRuleQuery{
			Caller: v1.Caller{Session: string(session)}, Name: "Elsewhere",
			Kind: domain.LibraryRuleCollection, ModuleID: "not-installed",
			CatalogID: "top", NativeType: "movie",
		})
		requireCategory(t, err, contracts.NotFound)
	})
}

func TestRunLibraryMaintenance(t *testing.T) {
	ctx := context.Background()

	// The exit criterion, in one test: two rules, a run, new titles in the
	// library that nobody pressed Add on, a second run that adds no duplicates,
	// and an account of both.
	t.Run("two rules fill the library, and a second run adds no duplicates", func(t *testing.T) {
		first := &catalogModule{id: "stremio", titles: []string{"Arrival", "Dune"}}
		second := &catalogModule{id: "tmdb", titles: []string{"Contact"}}
		svc, db, session := rulesFixture(t, first, second)
		caller := v1.Caller{Session: string(session)}

		createCollectionRule(t, svc, session, "Top films", "stremio", 0)
		createCollectionRule(t, svc, session, "TMDB top", "tmdb", 0)

		run, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller})
		if err != nil {
			t.Fatalf("RunLibraryMaintenance: %v", err)
		}
		if run.Rules != 2 || run.Created != 3 || run.Refreshed != 0 || run.Failed != 0 {
			t.Fatalf("first run = %+v, want two rules and three created", run)
		}

		library, err := svc.ListLibrary(ctx, app.ListLibraryQuery{Caller: caller})
		if err != nil {
			t.Fatalf("ListLibrary: %v", err)
		}
		if library.Total != 3 {
			t.Fatalf("library holds %d works after the first run, want 3", library.Total)
		}

		again, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller})
		if err != nil {
			t.Fatalf("second RunLibraryMaintenance: %v", err)
		}
		if again.Created != 0 || again.Refreshed != 3 {
			t.Fatalf("second run = %+v, want nothing created and three refreshed", again)
		}

		db.mu.Lock()
		nodes := len(db.nodes)
		db.mu.Unlock()
		if nodes != 3 {
			t.Fatalf("the second run left %d works, want the same 3 — it duplicated the library", nodes)
		}
	})

	// platform#13's whole reason for the system principal: the write must not be
	// attributed to, or fail because of, whoever wrote the rule.
	t.Run("materialises as the system principal, not as the caller", func(t *testing.T) {
		mod := &catalogModule{id: "stremio", titles: []string{"Arrival"}}
		svc, _, session := rulesFixture(t, mod)
		createCollectionRule(t, svc, session, "Top films", "stremio", 0)

		if _, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{
			Caller: v1.Caller{Session: string(session)},
		}); err != nil {
			t.Fatalf("RunLibraryMaintenance: %v", err)
		}
		if len(mod.callers) != 1 {
			t.Fatalf("the module was invoked %d times, want once", len(mod.callers))
		}
		if mod.callers[0] == string(session) {
			t.Fatal("the import acted as the administrator who triggered it, not as the install")
		}
		if mod.callers[0] != svc.SystemCaller().Session {
			t.Fatalf("the import acted as %q, want the system principal", mod.callers[0])
		}
	})

	// Best-effort per item: one failure logs and continues, because a run that
	// produced the rest has succeeded.
	t.Run("one item failing does not stop the rule", func(t *testing.T) {
		mod := &catalogModule{id: "stremio", titles: []string{"Arrival", "Dune", "Contact"}, failTitle: "Dune"}
		svc, _, session := rulesFixture(t, mod)
		rule := createCollectionRule(t, svc, session, "Top films", "stremio", 0)

		run, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{
			Caller: v1.Caller{Session: string(session)},
		})
		if err != nil {
			t.Fatalf("RunLibraryMaintenance: %v", err)
		}
		if run.Created != 2 || run.Failed != 1 {
			t.Fatalf("run = %+v, want two created and one failed", run)
		}

		// And the account is on the rule, where the administrator managing it
		// will read it — not only in a log behind expert mode.
		rules, err := svc.ListLibraryRules(ctx, app.ListLibraryRulesQuery{Caller: v1.Caller{Session: string(session)}})
		if err != nil {
			t.Fatalf("ListLibraryRules: %v", err)
		}
		var found domain.LibraryRuleRun
		for _, listing := range rules.Rules {
			if listing.Rule.ID == rule.ID {
				found = listing.Rule.LastRun
			}
		}
		if found.NeverRun() {
			t.Fatal("the rule does not say it ran")
		}
		if found.Matched != 3 || found.Created != 2 || found.Failed != 1 {
			t.Fatalf("the rule's account = %+v, want 3 matched, 2 created, 1 failed", found)
		}
	})

	// The bound is what keeps a household's upstream load predictable, and the
	// run has to say when it stopped on it rather than looking like a rule that
	// does not work.
	t.Run("a run stops on its budget and says so", func(t *testing.T) {
		mod := &catalogModule{id: "stremio", titles: []string{"A", "B", "C", "D", "E"}}
		svc, db, session := rulesFixture(t, mod)
		db.seedActiveConfig(t, map[string]any{"library.maintenance.items_per_run": 2})
		createCollectionRule(t, svc, session, "Top films", "stremio", 0)

		run, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{
			Caller: v1.Caller{Session: string(session)},
		})
		if err != nil {
			t.Fatalf("RunLibraryMaintenance: %v", err)
		}
		if run.Created != 2 {
			t.Fatalf("run created %d, want the budget of 2", run.Created)
		}
		if run.Skipped != 3 {
			t.Fatalf("run skipped %d, want the 3 the budget did not reach", run.Skipped)
		}
		if !run.Exhausted {
			t.Fatal("the run spent its budget and did not say so")
		}
	})

	t.Run("a paused rule is not evaluated", func(t *testing.T) {
		mod := &catalogModule{id: "stremio", titles: []string{"Arrival"}}
		svc, _, session := rulesFixture(t, mod)
		caller := v1.Caller{Session: string(session)}
		rule := createCollectionRule(t, svc, session, "Top films", "stremio", 0)

		if _, err := svc.SetLibraryRuleEnabled(ctx, app.SetLibraryRuleEnabledCommand{
			Caller: caller, RuleID: rule.ID, Enabled: false,
		}); err != nil {
			t.Fatalf("SetLibraryRuleEnabled: %v", err)
		}
		run, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller})
		if err != nil {
			t.Fatalf("RunLibraryMaintenance: %v", err)
		}
		if run.Rules != 0 || run.Created != 0 {
			t.Fatalf("run = %+v, want a paused rule to be left alone", run)
		}
	})
}

// A rule survives its module being uninstalled: degraded and visibly so, never
// deleted (platform#60). This is the case an extension being removable at runtime
// makes ordinary rather than exotic.
func TestALibraryRuleSurvivesItsModule(t *testing.T) {
	ctx := context.Background()
	mod := &catalogModule{id: "stremio", titles: []string{"Arrival"}}
	svc, _, registry, session := rulesFixtureWithRegistry(t, mod)
	caller := v1.Caller{Session: string(session)}

	createCollectionRule(t, svc, session, "Top films", "stremio", 0)
	if _, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller}); err != nil {
		t.Fatalf("RunLibraryMaintenance: %v", err)
	}

	// The extension is removed. Nothing about the rule changes.
	registry.Unregister("stremio")

	rules, err := svc.ListLibraryRules(ctx, app.ListLibraryRulesQuery{Caller: caller})
	if err != nil {
		t.Fatalf("ListLibraryRules: %v", err)
	}
	if len(rules.Rules) != 1 {
		t.Fatalf("the rule was lost with its module: %d rules remain", len(rules.Rules))
	}
	if rules.Rules[0].Available {
		t.Fatal("the rule reads as healthy with its module uninstalled")
	}

	// And the titles it already added stay. Rules add and never remove, and that
	// holds when the module behind one goes away.
	library, err := svc.ListLibrary(ctx, app.ListLibraryQuery{Caller: caller})
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if library.Total != 1 {
		t.Fatalf("library holds %d works, want the one the rule added to survive", library.Total)
	}

	// The run records why it could not be evaluated, rather than reporting a
	// clean run that found nothing.
	run, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller})
	if err != nil {
		t.Fatalf("RunLibraryMaintenance with a missing module: %v", err)
	}
	if run.Rules != 1 || run.Created != 0 {
		t.Fatalf("run = %+v", run)
	}
	after, err := svc.ListLibraryRules(ctx, app.ListLibraryRulesQuery{Caller: caller})
	if err != nil {
		t.Fatalf("ListLibraryRules: %v", err)
	}
	if after.Rules[0].Rule.LastRun.Error == "" {
		t.Fatal("the run recorded no reason for a rule it could not evaluate")
	}
}

// Deleting a rule withdraws the statement and keeps everything it materialised.
// Silently deleting what somebody half-watched is the worst thing this feature
// could do, and that includes doing it as a side effect of tidying up rules.
func TestDeletingALibraryRuleKeepsWhatItAdded(t *testing.T) {
	ctx := context.Background()
	mod := &catalogModule{id: "stremio", titles: []string{"Arrival", "Dune"}}
	svc, _, session := rulesFixture(t, mod)
	caller := v1.Caller{Session: string(session)}

	rule := createCollectionRule(t, svc, session, "Top films", "stremio", 0)
	if _, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller}); err != nil {
		t.Fatalf("RunLibraryMaintenance: %v", err)
	}
	if err := svc.DeleteLibraryRule(ctx, app.DeleteLibraryRuleCommand{Caller: caller, RuleID: rule.ID}); err != nil {
		t.Fatalf("DeleteLibraryRule: %v", err)
	}

	rules, err := svc.ListLibraryRules(ctx, app.ListLibraryRulesQuery{Caller: caller})
	if err != nil {
		t.Fatalf("ListLibraryRules: %v", err)
	}
	if len(rules.Rules) != 0 {
		t.Fatalf("the rule survived deletion: %+v", rules.Rules)
	}
	library, err := svc.ListLibrary(ctx, app.ListLibraryQuery{Caller: caller})
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if library.Total != 2 {
		t.Fatalf("deleting the rule left %d works, want the 2 it had added", library.Total)
	}
}

func TestListLibraryPagesAndCounts(t *testing.T) {
	ctx := context.Background()
	mod := &catalogModule{id: "stremio", titles: []string{"Arrival", "Blade Runner", "Contact", "Dune", "Elysium"}}
	svc, _, session := rulesFixture(t, mod)
	caller := v1.Caller{Session: string(session)}

	createCollectionRule(t, svc, session, "Top films", "stremio", 0)
	if _, err := svc.RunLibraryMaintenance(ctx, app.RunLibraryMaintenanceCommand{Caller: caller}); err != nil {
		t.Fatalf("RunLibraryMaintenance: %v", err)
	}

	page, err := svc.ListLibrary(ctx, app.ListLibraryQuery{Caller: caller, Limit: 2})
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(page.Works) != 2 {
		t.Fatalf("page held %d works, want 2", len(page.Works))
	}
	// The count is of the library, not of the page. It is the one thing this
	// screen can say that no provider-backed screen can.
	if page.Total != 5 {
		t.Fatalf("Total = %d, want the whole library", page.Total)
	}
	if !page.HasMore() {
		t.Fatal("HasMore said this was the last page of five over two")
	}

	last, err := svc.ListLibrary(ctx, app.ListLibraryQuery{Caller: caller, Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("ListLibrary(offset=4): %v", err)
	}
	if len(last.Works) != 1 || last.HasMore() {
		t.Fatalf("last page = %+v, want one work and no more", last)
	}
}
