// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"encoding/json"
	"testing"

	sdui "github.com/mosaic-media/mosaic-sdui/sdui"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// fakeQueries stands in for the application query surface, so the screen
// builders are tested without a full Service.
type fakeQueries struct {
	results          []v1.SearchResult
	catalogs         []app.ModuleCatalog
	items            []v1.CatalogItem
	node             v1.Node
	children         []v1.Node
	previewMeta      v1.ContentMetadata
	previewInLibrary bool
	previewNodeID    v1.NodeID
	settingsUI          []byte
	gotText             string
	gotCatalogID        string
	gotNodeID           v1.NodeID
	gotPreviewRef       v1.ContentRef
	gotSettingsModuleID string
}

func (f *fakeQueries) SearchAvailableContent(_ context.Context, q app.SearchAvailableContentQuery) (app.SearchAvailableContentResult, error) {
	f.gotText = q.Text
	return app.SearchAvailableContentResult{Results: f.results}, nil
}

func (f *fakeQueries) ListModuleCatalogs(_ context.Context, _ app.ListModuleCatalogsQuery) (app.ListModuleCatalogsResult, error) {
	return app.ListModuleCatalogsResult{Catalogs: f.catalogs}, nil
}

func (f *fakeQueries) ListCatalogItems(_ context.Context, q app.ListCatalogItemsQuery) (app.ListCatalogItemsResult, error) {
	f.gotCatalogID = q.CatalogID
	return app.ListCatalogItemsResult{Items: f.items}, nil
}

func (f *fakeQueries) GetContentNode(_ context.Context, q v1.GetContentNodeQuery) (v1.GetContentNodeResult, error) {
	f.gotNodeID = q.NodeID
	return v1.GetContentNodeResult{Node: f.node, Children: f.children}, nil
}

func (f *fakeQueries) PreviewContent(_ context.Context, q app.PreviewContentQuery) (app.PreviewContentResult, error) {
	f.gotPreviewRef = q.Ref
	return app.PreviewContentResult{Metadata: f.previewMeta, InLibrary: f.previewInLibrary, NodeID: f.previewNodeID}, nil
}

func (f *fakeQueries) ModuleSettingsUI(_ context.Context, q app.ModuleSettingsUIQuery) (app.ModuleSettingsUIResult, error) {
	f.gotSettingsModuleID = q.ModuleID
	return app.ModuleSettingsUIResult{ModuleID: q.ModuleID, UI: f.settingsUI}, nil
}

func render(t *testing.T, svc *Service, name string, params map[string]any) sdui.Node {
	t.Helper()
	node, err := svc.Render(context.Background(), name, v1.CallerFromSession("s-1"), params)
	if err != nil {
		t.Fatalf("Render(%q): %v", name, err)
	}
	return node
}

// find walks a node tree for the first node of the given type.
func find(n sdui.Node, typ string) (sdui.Node, bool) {
	if n.Type == typ {
		return n, true
	}
	for _, c := range n.Children {
		if got, ok := find(c, typ); ok {
			return got, true
		}
	}
	return sdui.Node{}, false
}

func findAll(n sdui.Node, typ string, acc *[]sdui.Node) {
	if n.Type == typ {
		*acc = append(*acc, n)
	}
	for _, c := range n.Children {
		findAll(c, typ, acc)
	}
}

func TestSearchScreenEmptyQueryPromptsWithNoBackendCall(t *testing.T) {
	fake := &fakeQueries{}
	svc := &Service{content: fake}

	node := render(t, svc, "search", nil)
	if node.Type != sdui.TypeScreen {
		t.Fatalf("root type = %q, want Screen", node.Type)
	}
	if _, ok := find(node, sdui.TypeSearchBar); !ok {
		t.Fatal("search screen has no SearchBar")
	}
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("an empty query must render an EmptyState prompt")
	}
	if fake.gotText != "" {
		t.Fatal("an empty query must not hit the search backend")
	}
}

func TestSearchScreenRendersResultsWithVirtualAndInLibraryActions(t *testing.T) {
	fake := &fakeQueries{results: []v1.SearchResult{
		{ // virtual — must carry a materialise (importContent) action
			Ref:   v1.ContentRef{Provider: "stremio", NativeID: "tt1254207", NativeType: "movie", MediaType: v1.MediaMovie, ExternalScheme: "imdb", ExternalID: "tt1254207"},
			Title: "Blade Runner 2049", Year: 2017,
		},
		{ // in-library — must carry a badge and a navigate action
			Ref:   v1.ContentRef{Provider: "stremio", NativeID: "tt0903747", NativeType: "series", MediaType: v1.MediaTVSeries},
			Title: "Breaking Bad", InLibrary: true, NodeID: "n-1",
		},
	}}
	svc := &Service{content: fake}

	node := render(t, svc, "search", map[string]any{"text": "blade"})
	if fake.gotText != "blade" {
		t.Fatalf("backend saw text %q, want the query forwarded", fake.gotText)
	}

	var cards []sdui.Node
	findAll(node, sdui.TypePosterCard, &cards)
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(cards))
	}

	// The virtual card opens a detail preview, carrying its ref (materialising
	// happens on that screen, not the card).
	virtual := cards[0]
	act, _ := virtual.Props["action"].(sdui.Action)
	if act.Kind != sdui.KindNavigate || act.Screen == nil || *act.Screen != "detail" {
		t.Fatalf("virtual card action = %+v, want Navigate detail", act)
	}
	ref, _ := act.Params["ref"].(map[string]any)
	if ref["externalId"] != "tt1254207" || ref["provider"] != "stremio" {
		t.Fatalf("detail ref = %+v, want the result's ref", ref)
	}

	// The in-library card navigates to detail and carries a badge.
	inLib := cards[1]
	if inLib.Props["badge"] != "In library" {
		t.Fatalf("in-library card badge = %v, want \"In library\"", inLib.Props["badge"])
	}
	libAct, _ := inLib.Props["action"].(sdui.Action)
	if libAct.Kind != sdui.KindNavigate || libAct.Screen == nil || *libAct.Screen != "detail" {
		t.Fatalf("in-library card action = %+v, want Navigate detail", libAct)
	}

	// The whole tree serializes to JSON (the wire form the client renders).
	if _, err := json.Marshal(node); err != nil {
		t.Fatalf("screen does not serialize: %v", err)
	}
}

func TestSearchScreenNoResultsShowsEmptyState(t *testing.T) {
	svc := &Service{content: &fakeQueries{results: nil}}
	node := render(t, svc, "search", map[string]any{"text": "zzz"})
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("no results must render an EmptyState")
	}
}

func TestUnknownScreenIsNotFound(t *testing.T) {
	svc := &Service{content: &fakeQueries{}}
	_, err := svc.Render(context.Background(), "nope", v1.CallerFromSession("s-1"), nil)
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("category = %s, want NotFound", got)
	}
}

func TestCollectionsScreenListsCatalogsAsNavigableRows(t *testing.T) {
	fake := &fakeQueries{catalogs: []app.ModuleCatalog{
		{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Popular Movies"}},
	}}
	node := render(t, &Service{content: fake}, "collections", nil)

	var buttons []sdui.Node
	findAll(node, sdui.TypeButton, &buttons)
	if len(buttons) != 1 {
		t.Fatalf("buttons = %d, want 1 per catalog", len(buttons))
	}
	act, _ := buttons[0].Props["action"].(sdui.Action)
	if act.Kind != sdui.KindNavigate || act.Screen == nil || *act.Screen != "catalog" {
		t.Fatalf("catalog row action = %+v, want Navigate catalog", act)
	}
	if act.Params["catalogId"] != "top" || act.Params["moduleId"] != "stremio" {
		t.Fatalf("navigate params = %+v, want the catalog's module and id", act.Params)
	}
}

func TestCollectionsScreenEmpty(t *testing.T) {
	node := render(t, &Service{content: &fakeQueries{}}, "collections", nil)
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("no catalogs must render an EmptyState")
	}
}

func TestCatalogScreenRendersItemsAsDetailLinks(t *testing.T) {
	fake := &fakeQueries{items: []v1.CatalogItem{
		{Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt1254207", NativeType: "movie", MediaType: v1.MediaMovie, ExternalScheme: "imdb", ExternalID: "tt1254207"}, Title: "Blade Runner 2049", Year: 2017},
	}}
	node := render(t, &Service{content: fake}, "catalog", map[string]any{"moduleId": "stremio", "catalogId": "top", "nativeType": "movie"})
	if fake.gotCatalogID != "top" {
		t.Fatalf("backend saw catalog %q, want the param forwarded", fake.gotCatalogID)
	}
	var cards []sdui.Node
	findAll(node, sdui.TypePosterCard, &cards)
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	act, _ := cards[0].Props["action"].(sdui.Action)
	if act.Kind != sdui.KindNavigate || act.Screen == nil || *act.Screen != "detail" {
		t.Fatalf("catalog item action = %+v, want Navigate detail", act)
	}
}

func TestVirtualDetailShowsAddToLibrary(t *testing.T) {
	fake := &fakeQueries{previewMeta: v1.ContentMetadata{
		Title: "Blade Runner 2049", Year: 2017, Overview: "A blade runner uncovers a secret.",
		Backdrop: "http://cdn/bd.jpg", Logo: "http://cdn/logo.png", Rating: 8.0, Runtime: "164 min",
		Cast: []v1.Person{{Name: "Ryan Gosling"}, {Name: "Harrison Ford"}}, Genres: []string{"Sci-Fi"},
	}}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": map[string]any{
		"provider": "stremio", "nativeId": "tt1254207", "nativeType": "movie",
		"mediaType": "movie", "externalScheme": "imdb", "externalId": "tt1254207",
	}})
	if fake.gotPreviewRef.NativeID != "tt1254207" {
		t.Fatalf("preview saw ref %+v, want the card's ref", fake.gotPreviewRef)
	}
	// The rich detail is a HeroBanner carrying the title, logo and the primary
	// action; the poster docks in its aside slot (ADR 0034).
	hero, ok := find(node, sdui.TypeHeroBanner)
	if !ok || hero.Props["title"] != "Blade Runner 2049" {
		t.Fatalf("hero = %+v, want the previewed title", hero.Props)
	}
	if hero.Props["logo"] == nil || hero.Props["logo"] == "" {
		t.Fatalf("hero has no logo prop; want the clearlogo bound")
	}
	// The sole library affordance is Add to library, in the hero's actions slot.
	actions := hero.Slots["actions"]
	if len(actions) != 1 || actions[0].Props["label"] != "Add to library" {
		t.Fatalf("hero actions = %+v, want a single Add to library button", actions)
	}
	act, _ := actions[0].Props["action"].(sdui.Action)
	if act.Kind != sdui.KindInvoke || act.Mutation == nil || *act.Mutation != "importContent" {
		t.Fatalf("add action = %+v, want Invoke importContent", act)
	}
	ref, _ := act.Input["ref"].(map[string]any)
	if ref["nativeId"] != "tt1254207" {
		t.Fatalf("add ref = %+v, want the previewed ref", ref)
	}
	// Top cast renders as PersonChips.
	var chips []sdui.Node
	findAll(node, sdui.TypePersonChip, &chips)
	if len(chips) != 2 {
		t.Fatalf("cast chips = %d, want 2", len(chips))
	}
	// The whole tree serializes to the wire form the client renders.
	if _, err := json.Marshal(node); err != nil {
		t.Fatalf("screen does not serialize: %v", err)
	}
}

func TestInLibraryDetailShowsInLibraryMarker(t *testing.T) {
	// An in-library ref renders the same rich detail from live metadata (ADR
	// 0034), differing only in the primary action — an In library marker, not
	// Add to library — and does not fall back to a structural node read.
	fake := &fakeQueries{
		previewInLibrary: true, previewNodeID: "n-9",
		previewMeta: v1.ContentMetadata{Title: "Already Here", Year: 2020},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": map[string]any{
		"provider": "stremio", "nativeId": "tt1", "nativeType": "movie", "mediaType": "movie",
	}})
	if fake.gotNodeID != "" {
		t.Fatalf("in-library detail should render from metadata, not read node %q", fake.gotNodeID)
	}
	hero, ok := find(node, sdui.TypeHeroBanner)
	if !ok || hero.Props["title"] != "Already Here" {
		t.Fatalf("hero = %+v, want the metadata title", hero.Props)
	}
	actions := hero.Slots["actions"]
	if len(actions) != 1 || actions[0].Type != sdui.TypeBadge || actions[0].Props["label"] != "In library" {
		t.Fatalf("hero actions = %+v, want a single In library badge", actions)
	}
}

func TestSeriesDetailRendersEpisodesWithSeasonSelector(t *testing.T) {
	fake := &fakeQueries{previewMeta: v1.ContentMetadata{
		Title: "Avatar: The Last Airbender",
		Episodes: []v1.EpisodePreview{
			{Season: 1, Episode: 1, Title: "The Boy in the Iceberg", Overview: "Katara and Sokka find Aang."},
			{Season: 1, Episode: 2, Title: "The Avatar Returns", Overview: "Zuko attacks."},
			{Season: 2, Episode: 1, Title: "The Avatar State", Overview: "Aang trains."},
		},
	}}
	refParam := map[string]any{"provider": "stremio", "nativeId": "tt0417299", "nativeType": "series", "mediaType": "tv-series"}

	// Default (no season param) shows season 1's two episodes.
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": refParam})
	selector, ok := find(node, sdui.TypeSeasonSelector)
	if !ok {
		t.Fatal("series detail has no SeasonSelector")
	}
	seasons, _ := selector.Props["seasons"].([]map[string]any)
	if len(seasons) != 2 {
		t.Fatalf("season entries = %d, want 2", len(seasons))
	}
	if selector.Props["selected"] != "1" {
		t.Fatalf("default selected season = %v, want \"1\"", selector.Props["selected"])
	}
	var rows []sdui.Node
	findAll(node, sdui.TypeEpisodeRow, &rows)
	if len(rows) != 2 {
		t.Fatalf("season 1 episode rows = %d, want 2", len(rows))
	}
	if rows[0].Props["title"] != "The Boy in the Iceberg" || rows[0].Props["overview"] == nil {
		t.Fatalf("episode row = %+v, want the title and synopsis", rows[0].Props)
	}

	// The season param switches to season 2's single episode.
	node2 := render(t, &Service{content: fake}, "detail", map[string]any{"ref": refParam, "season": "2"})
	var rows2 []sdui.Node
	findAll(node2, sdui.TypeEpisodeRow, &rows2)
	if len(rows2) != 1 || rows2[0].Props["title"] != "The Avatar State" {
		t.Fatalf("season 2 rows = %+v, want the single S2 episode", rows2)
	}
	sel2, _ := find(node2, sdui.TypeSeasonSelector)
	if sel2.Props["selected"] != "2" {
		t.Fatalf("selected season = %v, want \"2\"", sel2.Props["selected"])
	}
}

func TestCatalogScreenRequiresParams(t *testing.T) {
	_, err := (&Service{content: &fakeQueries{}}).Render(context.Background(), "catalog", v1.CallerFromSession("s-1"), map[string]any{"moduleId": "stremio"})
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("category = %s, want InvalidArgument", got)
	}
}

func TestDetailScreenRendersHeaderAndChildren(t *testing.T) {
	fake := &fakeQueries{
		node: v1.Node{ID: "n-1", WorkID: "n-1", Kind: v1.NodeWork, MediaType: v1.MediaTVSeries, Title: "Breaking Bad"},
		children: []v1.Node{
			{ID: "n-2", Kind: v1.NodeContainer, MediaType: v1.MediaTVSeries, Title: "Season 1"},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"nodeId": "n-1"})
	if fake.gotNodeID != "n-1" {
		t.Fatalf("backend saw node %q, want n-1", fake.gotNodeID)
	}
	header, ok := find(node, sdui.TypeDetailHeader)
	if !ok || header.Props["title"] != "Breaking Bad" {
		t.Fatalf("detail header = %+v, want the node title", header.Props)
	}
	var cards []sdui.Node
	findAll(node, sdui.TypePosterCard, &cards)
	if len(cards) != 1 {
		t.Fatalf("child cards = %d, want 1 per child", len(cards))
	}
	act, _ := cards[0].Props["action"].(sdui.Action)
	if act.Kind != sdui.KindNavigate || act.Params["nodeId"] != "n-2" {
		t.Fatalf("child card action = %+v, want Navigate to the child's detail", act)
	}
}

func TestSettingsScreenHostsModuleUI(t *testing.T) {
	// The Platform hosts the module's contributed settings UINode verbatim (ADR
	// 0038): the settings screen renders whatever the module returned.
	moduleUI := `{"type":"Screen","props":{"title":"Stremio addons"},"children":[{"type":"Section","props":{"title":"Add an addon"}}]}`
	fake := &fakeQueries{settingsUI: []byte(moduleUI)}

	node := render(t, &Service{content: fake}, "settings", nil)
	if fake.gotSettingsModuleID != "stremio" {
		t.Fatalf("settings screen resolved module %q, want the stremio default", fake.gotSettingsModuleID)
	}
	if node.Type != sdui.TypeScreen || node.Props["title"] != "Stremio addons" {
		t.Fatalf("settings root = %+v, want the module's Screen", node.Props)
	}
	if _, ok := find(node, sdui.TypeSection); !ok {
		t.Fatal("settings screen did not render the module's section")
	}
}

func TestDetailScreenRequiresNodeId(t *testing.T) {
	_, err := (&Service{content: &fakeQueries{}}).Render(context.Background(), "detail", v1.CallerFromSession("s-1"), nil)
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("category = %s, want InvalidArgument", got)
	}
}
