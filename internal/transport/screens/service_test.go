// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"testing"

	sdui "github.com/mosaic-media/contracts/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// fakeQueries stands in for the application query surface, so the screen
// builders are tested without a full Service. The real query surface
// (*app.Service) serves concurrent requests, and homeScreen fans its catalog
// reads out across goroutines, so the fake guards its captured-arg fields with a
// mutex — as any concurrency-safe stand-in must.
type fakeQueries struct {
	playbackState v1.PlaybackState
	playablePart  v1.Part

	results          []v1.SearchResult
	catalogs         []app.ModuleCatalog
	items            []v1.CatalogItem
	node             v1.Node
	children         []v1.Node
	previewMeta      v1.ContentMetadata
	previewInLibrary bool
	previewNodeID    v1.NodeID
	settingsUI       []byte
	// settingsModules is what ListSettingsModules reports — the modules the
	// settings index offers a way into.
	settingsModules []app.SettingsModule

	// installedExtensions and availableExtensions back the extensions screen;
	// availableErr lets a test drive the repository-unreachable path.
	installedExtensions []app.InstalledExtension
	availableExtensions []app.ExtensionCatalogueEntry
	availableErr        error

	// inProgress is what ListInProgress reports — the continue-watching rail's
	// input. playbackStates is what ListPlaybackStates reports for the watched
	// marks; the fake returns the whole map and lets the caller pick the ids.
	inProgress     []v1.InProgressItem
	playbackStates map[v1.NodeID]v1.PlaybackState
	// childrenByNode, when set for a node id, is what GetContentNode returns as
	// that node's children — so a tree walk (series → seasons → episodes) can be
	// exercised. Absent an entry, GetContentNode falls back to the flat children.
	childrenByNode map[v1.NodeID][]v1.Node

	// canReadTelemetry is what CallerCan reports — the fake's way of saying
	// "this caller holds telemetry.read", which is what decides whether the
	// expert-mode affordance is drawn at all.
	canReadTelemetry bool
	expertModeOn     bool
	logs             []domain.TelemetryLogRecord
	traces           []domain.TelemetryTraceSummary
	spans            []domain.TelemetrySpanRecord

	mu                  sync.Mutex
	gotText             string
	gotCatalogID        string
	gotNodeID           v1.NodeID
	gotPreviewRef       v1.ContentRef
	gotSettingsModuleID string
	gotLogFilter        domain.TelemetryLogFilter
	gotTraceFilter      domain.TelemetryTraceFilter
	gotTraceID          string
}

func (f *fakeQueries) ExpertModeEnabled(context.Context, v1.Caller) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.expertModeOn
}

func (f *fakeQueries) CallerCan(context.Context, v1.Caller, policy.Action, string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canReadTelemetry
}

func (f *fakeQueries) QueryTelemetryLogs(_ context.Context, q app.QueryTelemetryLogsQuery) (app.QueryTelemetryLogsResult, error) {
	f.mu.Lock()
	f.gotLogFilter = q.Filter
	f.mu.Unlock()
	return app.QueryTelemetryLogsResult{Records: f.logs}, nil
}

func (f *fakeQueries) ListTraces(_ context.Context, q app.ListTracesQuery) (app.ListTracesResult, error) {
	f.mu.Lock()
	f.gotTraceFilter = q.Filter
	f.mu.Unlock()
	return app.ListTracesResult{Traces: f.traces}, nil
}

func (f *fakeQueries) GetTrace(_ context.Context, q app.GetTraceQuery) (app.GetTraceResult, error) {
	f.mu.Lock()
	f.gotTraceID = q.TraceID
	f.mu.Unlock()
	return app.GetTraceResult{Spans: f.spans, Logs: f.logs}, nil
}

func (f *fakeQueries) SearchAvailableContent(_ context.Context, q app.SearchAvailableContentQuery) (app.SearchAvailableContentResult, error) {
	f.mu.Lock()
	f.gotText = q.Text
	f.mu.Unlock()
	return app.SearchAvailableContentResult{Results: f.results}, nil
}

func (f *fakeQueries) ListModuleCatalogs(_ context.Context, _ app.ListModuleCatalogsQuery) (app.ListModuleCatalogsResult, error) {
	return app.ListModuleCatalogsResult{Catalogs: f.catalogs}, nil
}

func (f *fakeQueries) ListCatalogItems(_ context.Context, q app.ListCatalogItemsQuery) (app.ListCatalogItemsResult, error) {
	f.mu.Lock()
	f.gotCatalogID = q.CatalogID
	f.mu.Unlock()
	return app.ListCatalogItemsResult{Items: f.items}, nil
}

func (f *fakeQueries) GetContentNode(_ context.Context, q v1.GetContentNodeQuery) (v1.GetContentNodeResult, error) {
	f.mu.Lock()
	f.gotNodeID = q.NodeID
	f.mu.Unlock()
	if kids, ok := f.childrenByNode[q.NodeID]; ok {
		node := f.node
		node.ID = q.NodeID
		return v1.GetContentNodeResult{Node: node, Children: kids}, nil
	}
	return v1.GetContentNodeResult{Node: f.node, Children: f.children}, nil
}

// playablePart, when set, is what FirstPlayablePart reports — the fake's way of
// saying "this library item has bytes", which is what gates the Play button.
func (f *fakeQueries) FirstPlayablePart(_ context.Context, _ v1.Caller, _ v1.NodeID) (v1.Part, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.playablePart.ID == "" {
		return v1.Part{}, false, nil
	}
	return f.playablePart, true, nil
}

// playbackState, when set, is what GetPlaybackState reports — the fake's way of
// saying "this viewer already started this", which is what turns Play into
// Resume (ADR 0046).
func (f *fakeQueries) GetPlaybackState(_ context.Context, _ v1.GetPlaybackStateQuery) (v1.GetPlaybackStateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.playbackState.Position == 0 && !f.playbackState.Finished {
		return v1.GetPlaybackStateResult{}, nil
	}
	return v1.GetPlaybackStateResult{State: f.playbackState, Found: true}, nil
}

func (f *fakeQueries) ListInProgress(_ context.Context, _ v1.ListInProgressQuery) (v1.ListInProgressResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return v1.ListInProgressResult{Items: f.inProgress}, nil
}

func (f *fakeQueries) ListPlaybackStates(_ context.Context, _ v1.ListPlaybackStatesQuery) (v1.ListPlaybackStatesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return v1.ListPlaybackStatesResult{States: f.playbackStates}, nil
}

func (f *fakeQueries) PreviewContent(_ context.Context, q app.PreviewContentQuery) (app.PreviewContentResult, error) {
	f.mu.Lock()
	f.gotPreviewRef = q.Ref
	f.mu.Unlock()
	return app.PreviewContentResult{Metadata: f.previewMeta, InLibrary: f.previewInLibrary, NodeID: f.previewNodeID}, nil
}

func (f *fakeQueries) ModuleSettingsUI(_ context.Context, q app.ModuleSettingsUIQuery) (app.ModuleSettingsUIResult, error) {
	f.mu.Lock()
	f.gotSettingsModuleID = q.ModuleID
	f.mu.Unlock()
	return app.ModuleSettingsUIResult{ModuleID: q.ModuleID, UI: f.settingsUI}, nil
}

func (f *fakeQueries) ListSettingsModules(context.Context, app.ListSettingsModulesQuery) (app.ListSettingsModulesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.ListSettingsModulesResult{Modules: f.settingsModules}, nil
}

func (f *fakeQueries) ListInstalledExtensions(context.Context, app.ListInstalledExtensionsQuery) ([]app.InstalledExtension, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installedExtensions, nil
}

func (f *fakeQueries) ListAvailableExtensions(context.Context, app.ListAvailableExtensionsQuery) ([]app.ExtensionCatalogueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.availableExtensions, f.availableErr
}

func render(t *testing.T, svc *Service, name string, params map[string]any) sdui.Node {
	t.Helper()
	node, err := svc.Render(context.Background(), name, v1.CallerFromSession("s-1"), params)
	if err != nil {
		t.Fatalf("Render(%q): %v", name, err)
	}
	return node
}

// find walks a node tree (children and slots) for the first node of the type.
func find(n sdui.Node, typ string) (sdui.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.GetType() == typ {
		return n, true
	}
	for _, c := range n.GetChildren() {
		if got, ok := find(c, typ); ok {
			return got, true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if got, ok := find(c, typ); ok {
				return got, true
			}
		}
	}
	return nil, false
}

func findAll(n sdui.Node, typ string, acc *[]sdui.Node) {
	if n == nil {
		return
	}
	if n.GetType() == typ {
		*acc = append(*acc, n)
	}
	for _, c := range n.GetChildren() {
		findAll(c, typ, acc)
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			findAll(c, typ, acc)
		}
	}
}

// prop reads a node's prop from the protobuf Struct (ADR 0044 — props is an open
// Struct, decoded to a Go map for assertions).
// findNavItem finds a settings nav row anywhere in the tree by its label. The
// rows live in the frame's `nav` slot, which is why this walks slots too.
func findNavItem(n sdui.Node, label string) (sdui.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.GetType() == "SettingsNavItem" && prop(n, "label") == label {
		return n, true
	}
	for _, c := range n.GetChildren() {
		if got, ok := findNavItem(c, label); ok {
			return got, true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if got, ok := findNavItem(c, label); ok {
				return got, true
			}
		}
	}
	return nil, false
}

// findButton finds a Button anywhere in the tree by its label.
func findButton(n sdui.Node, label string) (sdui.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.GetType() == sdui.TypeButton && prop(n, "label") == label {
		return n, true
	}
	for _, c := range n.GetChildren() {
		if got, ok := findButton(c, label); ok {
			return got, true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if got, ok := findButton(c, label); ok {
				return got, true
			}
		}
	}
	return nil, false
}

func prop(n sdui.Node, key string) any { return n.GetProps().AsMap()[key] }

// actionOf reads the action riding in a node's open props bag (JSON-in-Struct).
func actionOf(n sdui.Node) map[string]any {
	a, _ := prop(n, "action").(map[string]any)
	return a
}

// slotNodes returns the nodes of a named slot.
func slotNodes(n sdui.Node, name string) []sdui.Node { return n.GetSlots()[name].GetNodes() }

// mapAt reads a nested object out of a decoded props/action map.
func mapAt(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func TestSearchScreenEmptyQueryPromptsWithNoBackendCall(t *testing.T) {
	fake := &fakeQueries{}
	svc := &Service{content: fake}

	node := render(t, svc, "search", nil)
	if node.Type != sdui.TypeScreen {
		t.Fatalf("root type = %q, want Screen", node.Type)
	}
	// The search screen carries its OWN SearchBar (shown on mobile, where search
	// is a tab; desktop hides it and uses the top-bar search). An empty query
	// renders a prompt and hits no backend.
	if _, ok := find(node, sdui.TypeSearchBar); !ok {
		t.Fatal("search screen must carry its own SearchBar (the mobile search field)")
	}
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("an empty query must render an EmptyState prompt")
	}
	if fake.gotText != "" {
		t.Fatal("an empty query must not hit the search backend")
	}
}

func TestHomeScreenRendersHeroAndCatalogRows(t *testing.T) {
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "movie", Name: "Popular Movies"}},
			{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "series", Name: "Popular Series"}},
		},
		items: []v1.CatalogItem{
			{Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}, Title: "A Movie", Year: 2020},
		},
		previewMeta: v1.ContentMetadata{Title: "A Movie", Backdrop: "http://cdn/bd.jpg", Overview: "Synopsis.", Rating: 8.0},
	}
	node := render(t, &Service{content: fake}, "home", nil)

	// A hero (from the first catalog's first item, enriched via PreviewContent).
	hero, ok := find(node, sdui.TypeHeroBanner)
	if !ok {
		t.Fatal("home screen has no hero")
	}
	if prop(hero, "title") != "A Movie" {
		t.Fatalf("hero title = %v, want the enriched item title", prop(hero, "title"))
	}
	if fake.gotPreviewRef.NativeID != "tt1" {
		t.Fatalf("hero enriched ref = %+v, want the first item", fake.gotPreviewRef)
	}
	// A titled row per catalog, each a carousel of cards.
	var sections, carousels []sdui.Node
	findAll(node, sdui.TypeSection, &sections)
	findAll(node, sdui.TypeCarousel, &carousels)
	if len(sections) != 2 || len(carousels) != 2 {
		t.Fatalf("sections=%d carousels=%d, want 2 each (one per catalog)", len(sections), len(carousels))
	}
}

func TestHomeScreenEmptyWithoutCatalogs(t *testing.T) {
	node := render(t, &Service{content: &fakeQueries{}}, "home", nil)
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("home with no catalogs must render an EmptyState")
	}
}

func TestHomeScreenRendersContinueWatchingRail(t *testing.T) {
	fake := &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "stremio", Catalog: v1.Catalog{ID: "top", NativeType: "series", Name: "Popular Series"}},
		},
		items: []v1.CatalogItem{
			{Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt1", NativeType: "series", MediaType: v1.MediaTVSeries}, Title: "A Series"},
		},
		previewMeta: v1.ContentMetadata{Title: "A Series", Backdrop: "http://cdn/bd.jpg"},
		// The Work the in-progress episode belongs to, with stored art (ADR 0071).
		node: v1.Node{
			ID: "work-1", WorkID: "work-1", Kind: v1.NodeWork,
			MediaType: v1.MediaTVSeries, Title: "The Series",
			Artwork: v1.Artwork{Poster: "http://cdn/poster.jpg"},
		},
		inProgress: []v1.InProgressItem{{
			Node: v1.Node{
				ID: "ep-3", WorkID: "work-1", ItemType: v1.ItemEpisode,
				MediaType: v1.MediaTVSeries, Title: "The Third Episode",
			},
			State: v1.PlaybackState{NodeID: "ep-3", PartID: "part-9", Position: 30 * time.Minute, Duration: 60 * time.Minute},
		}},
	}
	node := render(t, &Service{content: fake}, "home", nil)

	var sections []sdui.Node
	findAll(node, sdui.TypeSection, &sections)
	var rail sdui.Node
	for _, s := range sections {
		if prop(s, "title") == "Continue watching" {
			rail = s
		}
	}
	if rail == nil {
		t.Fatal("home with an in-progress item has no Continue watching rail")
	}

	card, ok := find(rail, sdui.TypePosterCard)
	if !ok {
		t.Fatal("continue-watching rail has no card")
	}
	// The series poster and title come from the Work; the episode is named
	// beneath it.
	if got, _ := prop(card, "title").(string); got != "The Series" {
		t.Fatalf("card title = %q, want the work title", got)
	}
	if got, _ := prop(card, "subtitle").(string); got != "The Third Episode" {
		t.Fatalf("card subtitle = %q, want the episode title", got)
	}
	// A resume-progress fraction (30 of 60 minutes).
	if got, _ := prop(card, "progress").(float64); got != 0.5 {
		t.Fatalf("card progress = %v, want 0.5", prop(card, "progress"))
	}
	// The tap resumes rather than navigating — a node cannot open a rich detail
	// (ADR 0071), so the card carries a play action, not a navigation.
	if actionOf(card) == nil {
		t.Fatal("continue-watching card has no action to resume")
	}
}

func TestSeriesDetailMarksWatchedEpisodes(t *testing.T) {
	fake := &fakeQueries{
		previewInLibrary: true,
		previewNodeID:    "series-1",
		previewMeta: v1.ContentMetadata{
			Title: "The Series",
			Episodes: []v1.EpisodePreview{
				{Season: 1, Episode: 1, Title: "Pilot"},
				{Season: 1, Episode: 2, Title: "Second"},
				{Season: 1, Episode: 3, Title: "Third"},
			},
		},
		// The materialised tree: a season container, then episode items, each
		// carrying its number as NaturalOrder — the bridge from the live preview's
		// (season, episode) back to the nodes playback state is keyed under.
		childrenByNode: map[v1.NodeID][]v1.Node{
			"series-1": {{ID: "s1", Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason, NaturalOrder: 1}},
			"s1": {
				{ID: "e1", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 1},
				{ID: "e2", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 2},
				{ID: "e3", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 3},
			},
		},
		// e1 is finished (watched); e2 is started but not finished; e3 unseen.
		playbackStates: map[v1.NodeID]v1.PlaybackState{
			"e1": {NodeID: "e1", Finished: true},
			"e2": {NodeID: "e2", Position: 5 * time.Minute},
		},
	}
	ref := map[string]any{"provider": "stremio", "nativeId": "tt1", "nativeType": "series", "mediaType": string(v1.MediaTVSeries)}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"ref": ref})

	var rows []sdui.Node
	findAll(node, sdui.TypeEpisodeRow, &rows)
	if len(rows) != 3 {
		t.Fatalf("got %d episode rows, want 3", len(rows))
	}
	watched := map[string]bool{}
	for _, r := range rows {
		title, _ := prop(r, "title").(string)
		watched[title] = prop(r, "watched") == true
	}
	if !watched["Pilot"] {
		t.Error("a finished episode should be marked watched")
	}
	if watched["Second"] {
		t.Error("a started-but-unfinished episode must not be marked watched")
	}
	if watched["Third"] {
		t.Error("an unseen episode must not be marked watched")
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
	act := actionOf(virtual)
	if act["kind"] != sdui.KindNavigate || act["screen"] != "detail" {
		t.Fatalf("virtual card action = %+v, want Navigate detail", act)
	}
	ref := mapAt(mapAt(act, "params"), "ref")
	if ref["externalId"] != "tt1254207" || ref["provider"] != "stremio" {
		t.Fatalf("detail ref = %+v, want the result's ref", ref)
	}

	// The in-library card navigates to detail and carries a badge.
	inLib := cards[1]
	if prop(inLib, "badge") != "In library" {
		t.Fatalf("in-library card badge = %v, want \"In library\"", prop(inLib, "badge"))
	}
	libAct := actionOf(inLib)
	if libAct["kind"] != sdui.KindNavigate || libAct["screen"] != "detail" {
		t.Fatalf("in-library card action = %+v, want Navigate detail", libAct)
	}

	// The whole tree serializes to JSON (the wire form the client renders).
	if _, err := protojson.Marshal(node); err != nil {
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
	act := actionOf(buttons[0])
	if act["kind"] != sdui.KindNavigate || act["screen"] != "catalog" {
		t.Fatalf("catalog row action = %+v, want Navigate catalog", act)
	}
	if mapAt(act, "params")["catalogId"] != "top" || mapAt(act, "params")["moduleId"] != "stremio" {
		t.Fatalf("navigate params = %+v, want the catalog's module and id", act)
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
	act := actionOf(cards[0])
	if act["kind"] != sdui.KindNavigate || act["screen"] != "detail" {
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
	// The rich detail is a DetailHero carrying the title, logo and the primary
	// action; a glass info panel docks in its aside slot (ADR 0034).
	hero, ok := find(node, "DetailHero")
	if !ok || prop(hero, "title") != "Blade Runner 2049" {
		t.Fatalf("hero = %+v, want the previewed title", hero.Props)
	}
	if prop(hero, "logo") == nil || prop(hero, "logo") == "" {
		t.Fatalf("hero has no logo prop; want the clearlogo bound")
	}
	// The sole library affordance is Add to library, in the hero's actions slot.
	actions := slotNodes(hero, "actions")
	if len(actions) != 1 || prop(actions[0], "label") != "Add to library" {
		t.Fatalf("hero actions = %+v, want a single Add to library button", actions)
	}
	act := actionOf(actions[0])
	if act["kind"] != sdui.KindInvoke || act["mutation"] != "importContent" {
		t.Fatalf("add action = %+v, want Invoke importContent", act)
	}
	ref := mapAt(mapAt(act, "input"), "ref")
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
	if _, err := protojson.Marshal(node); err != nil {
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
	hero, ok := find(node, "DetailHero")
	if !ok || prop(hero, "title") != "Already Here" {
		t.Fatalf("hero = %+v, want the metadata title", hero.Props)
	}
	// An in-library item carries the marker, and no Add to library — adding
	// what is already there is the affordance this screen must never offer.
	if _, ok := findButton(node, "Add to library"); ok {
		t.Error("an in-library item must not offer Add to library")
	}
	if _, ok := find(node, sdui.TypeBadge); !ok {
		t.Error("an in-library item must carry the In library marker")
	}
	// It also offers Refresh sources: a candidate set goes stale as releases
	// appear and disappear, and re-importing is how a user asks for the current
	// answer. Play is absent here because this fake has no playable part, which
	// is the gating TestDetailPlayAffordanceIsGatedOnAPartExisting covers.
	if _, ok := findButton(node, "Refresh sources"); !ok {
		t.Error("an in-library item must offer Refresh sources")
	}
	if _, ok := findButton(node, "Play"); ok {
		t.Error("Play must not appear when nothing in the tree has bytes")
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
	seasons, _ := prop(selector, "seasons").([]any)
	if len(seasons) != 2 {
		t.Fatalf("season entries = %d, want 2", len(seasons))
	}
	if prop(selector, "selected") != "1" {
		t.Fatalf("default selected season = %v, want \"1\"", prop(selector, "selected"))
	}
	var rows []sdui.Node
	findAll(node, sdui.TypeEpisodeRow, &rows)
	if len(rows) != 2 {
		t.Fatalf("season 1 episode rows = %d, want 2", len(rows))
	}
	if prop(rows[0], "title") != "The Boy in the Iceberg" || prop(rows[0], "overview") == nil {
		t.Fatalf("episode row = %+v, want the title and synopsis", rows[0].Props)
	}

	// The season param switches to season 2's single episode.
	node2 := render(t, &Service{content: fake}, "detail", map[string]any{"ref": refParam, "season": "2"})
	var rows2 []sdui.Node
	findAll(node2, sdui.TypeEpisodeRow, &rows2)
	if len(rows2) != 1 || prop(rows2[0], "title") != "The Avatar State" {
		t.Fatalf("season 2 rows = %+v, want the single S2 episode", rows2)
	}
	sel2, _ := find(node2, sdui.TypeSeasonSelector)
	if prop(sel2, "selected") != "2" {
		t.Fatalf("selected season = %v, want \"2\"", prop(sel2, "selected"))
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
	if !ok || prop(header, "title") != "Breaking Bad" {
		t.Fatalf("detail header = %+v, want the node title", header.Props)
	}
	var cards []sdui.Node
	findAll(node, sdui.TypePosterCard, &cards)
	if len(cards) != 1 {
		t.Fatalf("child cards = %d, want 1 per child", len(cards))
	}
	act := actionOf(cards[0])
	if act["kind"] != sdui.KindNavigate || mapAt(act, "params")["nodeId"] != "n-2" {
		t.Fatalf("child card action = %+v, want Navigate to the child's detail", act)
	}
}

func TestSettingsScreenHostsModuleUI(t *testing.T) {
	// The Platform hosts the module's contributed settings UINode verbatim (ADR
	// 0038): the settings screen renders whatever the module returned, in the
	// panel of a frame of the Platform's own.
	moduleUI := `{"type":"Screen","props":{"title":"AIOStreams"},"children":[{"type":"Section","props":{"title":"Instance"}}]}`
	fake := &fakeQueries{
		settingsUI:      []byte(moduleUI),
		settingsModules: []app.SettingsModule{{ModuleID: "aiostreams", Name: "AIOStreams"}},
	}

	node := render(t, &Service{content: fake}, "settings", map[string]any{"moduleId": "aiostreams"})
	if fake.gotSettingsModuleID != "aiostreams" {
		t.Fatalf("settings screen resolved module %q, want the requested one", fake.gotSettingsModuleID)
	}
	if node.GetType() != "SettingsFrame" {
		t.Fatalf("root type = %q, want the Platform's SettingsFrame", node.GetType())
	}
	// The module's own Screen container is replaced by the Platform's panel —
	// its padding is a whole page's and would apply twice inside the frame — and
	// its title becomes the panel heading. Everything the module put IN that
	// Screen is hosted verbatim, as a child of the frame rather than the nav
	// slot: the module fills the panel and cannot draw the chrome around it.
	if prop(node, "heading") != "AIOStreams" {
		t.Fatalf("panel heading = %v, want the module's screen title", prop(node, "heading"))
	}
	if _, ok := find(node, sdui.TypeScreen); ok {
		t.Fatalf("the module's Screen container survived into the panel: %+v", node)
	}
	section, ok := find(node, sdui.TypeSection)
	if !ok || prop(section, "title") != "Instance" {
		t.Fatal("settings screen did not render the module's section")
	}
	// The way back out is the Platform's, not the module's: the nav is on the
	// screen the whole time, with the open module's row marked active.
	row, ok := findNavItem(node, "AIOStreams")
	if !ok {
		t.Fatal("the hosted module has no row in the Platform's settings nav")
	}
	if prop(row, "active") != true {
		t.Fatalf("open module's nav row active = %v, want true", prop(row, "active"))
	}
}

// TestSettingsNavReachesEveryModuleWithAScreen is the client path a module's
// settings screen is owed. The host used to name one module by constant, so a
// second module's screen existed and nothing could open it.
func TestSettingsNavReachesEveryModuleWithAScreen(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: []byte(`{"type":"Screen","props":{"title":"AIOStreams"}}`),
		settingsModules: []app.SettingsModule{
			{ModuleID: "aiostreams", Name: "AIOStreams"},
			{ModuleID: "stremio", Name: "Stremio addon source"},
			{ModuleID: "tmdb", Name: "TMDB"},
		},
	}

	node := render(t, &Service{content: fake}, "settings", nil)
	for _, m := range fake.settingsModules {
		row, ok := findNavItem(node, m.Name)
		if !ok {
			t.Fatalf("settings nav has no way into %q", m.ModuleID)
		}
		act := actionOf(row)
		if act["kind"] != sdui.KindNavigate || mapAt(act, "params")["moduleId"] != m.ModuleID {
			t.Fatalf("%q nav row action = %+v, want a Navigate carrying its moduleId", m.ModuleID, act)
		}
	}
	// Opened with no params — as it is from the app nav — the panel lands on the
	// first section that has one rather than on an empty frame.
	if fake.gotSettingsModuleID != "aiostreams" {
		t.Fatalf("no-param settings rendered module %q, want the first nav section", fake.gotSettingsModuleID)
	}
}

// TestSettingsWithNoSectionsSaysSo covers a legitimate composition: a build with
// no settings-UI module, read by a caller with no install-level permission, has
// nothing to configure and must say so rather than render an empty frame.
func TestSettingsWithNoSectionsSaysSo(t *testing.T) {
	fake := &fakeQueries{}
	node := render(t, &Service{content: fake}, "settings", nil)
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("a settings screen over no sections must render an empty state")
	}
	if fake.gotSettingsModuleID != "" {
		t.Fatalf("rendering no sections invoked module %q", fake.gotSettingsModuleID)
	}
}

// TestSettingsSaysWhetherASectionWasAskedFor is what makes one payload serve a
// phone and a desktop. The frame renders as a list you drill into on a phone and
// as two panes on a desktop, and the difference it needs is not "is there a
// panel" — a no-param render resolves a default section so a desktop is not left
// with an empty pane. It is "did the caller ask for this section".
func TestSettingsSaysWhetherASectionWasAskedFor(t *testing.T) {
	newFake := func() *fakeQueries {
		return &fakeQueries{
			settingsUI:      []byte(`{"type":"Screen","props":{"title":"AIOStreams"}}`),
			settingsModules: []app.SettingsModule{{ModuleID: "aiostreams", Name: "AIOStreams"}},
		}
	}

	// Opened from the app nav: a default section renders, but nobody asked for it.
	defaulted := render(t, &Service{content: newFake()}, "settings", nil)
	if prop(defaulted, "selected") != false {
		t.Fatalf("selected = %v on a no-param render, want false — a phone must land on the list", prop(defaulted, "selected"))
	}

	// Tapped from the nav.
	asked := render(t, &Service{content: newFake()}, "settings", map[string]any{"moduleId": "aiostreams"})
	if prop(asked, "selected") != true {
		t.Fatalf("selected = %v after navigating to a module, want true", prop(asked, "selected"))
	}

	// Its own screen, always reached by asking for it.
	ext := render(t, &Service{content: newFake()}, "extensions", nil)
	if prop(ext, "selected") != true {
		t.Fatalf("selected = %v on the extensions screen, want true", prop(ext, "selected"))
	}
}

// TestSettingsNavIsGatedPerCaller pins ADR 0058's visibility rule onto the nav: a
// caller without the grant is not shown the affordance at all, rather than shown
// one that fails when they use it.
func TestSettingsNavIsGatedPerCaller(t *testing.T) {
	withPermission := render(t, &Service{content: &fakeQueries{canReadTelemetry: true}}, "settings", nil)
	if _, ok := findNavItem(withPermission, "Extensions"); !ok {
		t.Fatal("a caller holding module.read must be offered the Extensions section")
	}
	if _, ok := find(withPermission, "Toggle"); !ok {
		t.Fatal("a caller holding telemetry.read must be offered the expert-mode switch")
	}

	without := render(t, &Service{content: &fakeQueries{}}, "settings", nil)
	if _, ok := findNavItem(without, "Extensions"); ok {
		t.Fatal("Extensions is drawn for a caller who cannot read the module catalogue")
	}
	if _, ok := find(without, "Toggle"); ok {
		t.Fatal("the expert-mode switch is drawn for a caller who cannot read telemetry")
	}
}

// TestExtensionsScreenKeepsTheSettingsNav is the ADR 0081 screen inside the ADR
// 0038 frame: it stays its own screen (the catalogue is a network read), and
// opening it does not cost the nav that leads back to everything else.
func TestExtensionsScreenKeepsTheSettingsNav(t *testing.T) {
	fake := &fakeQueries{
		canReadTelemetry: true,
		settingsModules:  []app.SettingsModule{{ModuleID: "aiostreams", Name: "AIOStreams"}},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	if node.GetType() != "SettingsFrame" {
		t.Fatalf("root type = %q, want the extensions surface inside the settings frame", node.GetType())
	}
	row, ok := findNavItem(node, "Extensions")
	if !ok {
		t.Fatal("the extensions screen dropped the settings nav")
	}
	if prop(row, "active") != true {
		t.Fatalf("Extensions nav row active = %v, want true on its own screen", prop(row, "active"))
	}
	if _, ok := findNavItem(node, "AIOStreams"); !ok {
		t.Fatal("the extensions screen must keep the way back to the other sections")
	}
	// Listing the catalogue must not drag every module's settings UI in with it.
	if fake.gotSettingsModuleID != "" {
		t.Fatalf("the extensions screen rendered module %q's settings UI", fake.gotSettingsModuleID)
	}
}

// TestExtensionsScreenInstallAndUninstall is the browse-and-install surface
// (ADR 0081): an installed extension offers Uninstall, an available one that is
// not installed offers Install, and an available one that IS installed is not
// offered for install again.
func TestExtensionsScreenInstallAndUninstall(t *testing.T) {
	fake := &fakeQueries{
		installedExtensions: []app.InstalledExtension{{ModuleID: "stremio", Version: "v0.24.0"}},
		availableExtensions: []app.ExtensionCatalogueEntry{
			{Repository: "mosaic-official", ModuleID: "stremio", Name: "Stremio addon source", Version: "v0.24.0"},
			{Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0"},
		},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	un, ok := findButton(node, "Uninstall")
	if !ok {
		t.Fatal("installed extension has no Uninstall control")
	}
	act := actionOf(un)
	if act["kind"] != sdui.KindInvoke || act["mutation"] != "uninstallExtension" || mapAt(act, "input")["moduleId"] != "stremio" {
		t.Fatalf("Uninstall action = %+v, want an Invoke of uninstallExtension carrying stremio", act)
	}

	// aiostreams is available and not installed → offered; stremio is installed,
	// so it is not offered for install again. Each is a card, and the card's
	// control NAVIGATES: installing runs somebody else's binary, so it happens on
	// the far side of a screen that says what the thing does (see below).
	var cards []sdui.Node
	findAll(node, "ExtensionCard", &cards)
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want one for the installed module and one for the offered one", len(cards))
	}
	in, ok := findButton(node, "Install…")
	if !ok {
		t.Fatal("an available, not-installed extension has no Install control")
	}
	act = actionOf(in)
	if act["kind"] != sdui.KindOpenOverlay || act["surface"] != sdui.SurfaceModal {
		t.Fatalf("Install control = %+v, want an overlay over the catalogue", act)
	}
	// No RENDERED control installs: the only install action in the surface is
	// inside the confirmation the overlay carries. That is the whole point of it.
	if walkFindInvoke(node, "installExtension") {
		t.Fatal("the catalogue list emits installExtension directly; it must confirm first")
	}
}

// TestExtensionCardsDescribeWhatTheModuleCanDo — a card that names a module and
// nothing else asks a user to decide about a word. The capabilities come from
// the module's signed manifest, phrased in the Platform's vocabulary, so what is
// shown is what the module can actually do rather than what it says about itself.
func TestExtensionCardsDescribeWhatTheModuleCanDo(t *testing.T) {
	fake := &fakeQueries{
		availableExtensions: []app.ExtensionCatalogueEntry{{
			Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0",
			Provides: []string{"stream", "subtitles", "settings_ui"},
		}},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	card, ok := find(node, "ExtensionCard")
	if !ok {
		t.Fatal("no card for the offered extension")
	}
	// Two chips, not three: settings_ui is plumbing — every configurable module
	// declares it, and it says nothing about what this one is FOR.
	caps, _ := prop(card, "capabilities").([]any)
	if len(caps) != 2 {
		t.Fatalf("capability chips = %d, want one per declared role except settings_ui", len(caps))
	}
	summary, _ := prop(card, "summary").(string)
	for _, want := range []string{"streams", "subtitles"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not mention %q", summary, want)
		}
	}
	if strings.Contains(summary, "settings screen") {
		t.Fatalf("summary %q offers a settings screen as a reason to install something", summary)
	}
	if origin, _ := prop(card, "origin").(string); !strings.Contains(origin, "v0.3.0") || !strings.Contains(origin, "mosaic-official") {
		t.Fatalf("card origin = %q, want the version and the repository it comes from", origin)
	}
}

// TestExtensionCardPrefersTheModulesOwnWords — the capabilities say what a
// module can DO and the Platform derives them; only the author can say what it
// IS. So a description from the signed manifest wins, and a module that
// publishes none is still described rather than left blank.
func TestExtensionCardPrefersTheModulesOwnWords(t *testing.T) {
	fake := &fakeQueries{availableExtensions: []app.ExtensionCatalogueEntry{
		{
			Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0",
			Provides:    []string{"stream", "subtitles"},
			Description: "An independent aggregator that searches many sources at once.",
		},
		{
			Repository: "mosaic-official", ModuleID: "quiet", Name: "Quiet module", Version: "v1",
			Provides: []string{"artwork"},
		},
	}}
	node := render(t, &Service{content: fake}, "extensions", nil)

	var cards []sdui.Node
	findAll(node, "ExtensionCard", &cards)
	byName := map[string]sdui.Node{}
	for _, c := range cards {
		byName[prop(c, "name").(string)] = c
	}

	if got, _ := prop(byName["AIOStreams"], "summary").(string); got != "An independent aggregator that searches many sources at once." {
		t.Fatalf("card summary = %q, want the module's own sentence", got)
	}
	// No description: the capabilities still have to say something useful.
	got, _ := prop(byName["Quiet module"], "summary").(string)
	if !strings.Contains(got, "artwork") {
		t.Fatalf("undescribed module's summary = %q, want its capabilities", got)
	}
}

// TestInstallConfirmationCarriesTheInstall is where consent lives: the overlay
// the card opens names the capabilities and the provenance, and it is the only
// place the install action exists.
//
// An overlay rather than a screen because the decision is about the catalogue
// you are looking at — a screen would take it away and need a route back.
func TestInstallConfirmationCarriesTheInstall(t *testing.T) {
	fake := &fakeQueries{
		availableExtensions: []app.ExtensionCatalogueEntry{{
			Repository: "mosaic-official", ModuleID: "aiostreams", Name: "AIOStreams", Version: "v0.3.0",
			Provides: []string{"stream", "subtitles", "settings_ui"},
		}},
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	btn, ok := findButton(node, "Install…")
	if !ok {
		t.Fatalf("no Install control on the card: %s", nodeText(node))
	}
	act := actionOf(btn)
	if act["kind"] != sdui.KindOpenOverlay {
		t.Fatalf("Install action = %+v, want an overlay", act)
	}
	overlay, _ := act["node"].(map[string]any)
	if overlay == nil {
		t.Fatal("the overlay action carries no node — nothing would be presented")
	}
	text := fmt.Sprint(overlay)
	for _, want := range []string{"Streams", "Finds playable sources", "Subtitles", "mosaic-official", "v0.3.0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the confirmation does not state %q: %s", want, text)
		}
	}
	// settings_ui is plumbing and is not a capability a user weighs.
	if strings.Contains(text, "Adds its own section to Settings") {
		t.Fatalf("the confirmation lists the settings-screen role: %s", text)
	}
	if !mapHasInvoke(overlay, "installExtension", "aiostreams") {
		t.Fatalf("the confirmation carries no install for aiostreams: %s", text)
	}
	if !strings.Contains(text, "closeOverlay") {
		t.Fatalf("a confirmation with no way out is not a confirmation: %s", text)
	}
}

// mapHasInvoke reports whether a decoded node tree carries an Invoke of the
// mutation against the module id — the action rides inside the overlay's node,
// which is a decoded map rather than a UINode, so this walks maps.
func mapHasInvoke(v any, mutation, moduleID string) bool {
	switch x := v.(type) {
	case map[string]any:
		if x["kind"] == sdui.KindInvoke && x["mutation"] == mutation {
			if in, ok := x["input"].(map[string]any); ok && in["moduleId"] == moduleID {
				return true
			}
		}
		for _, val := range x {
			if mapHasInvoke(val, mutation, moduleID) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if mapHasInvoke(val, mutation, moduleID) {
				return true
			}
		}
	}
	return false
}

// walkFindInvoke reports whether any node in the tree carries an Invoke of the
// named mutation.
func walkFindInvoke(n sdui.Node, mutation string) bool {
	if n == nil {
		return false
	}
	if act := actionOf(n); act["kind"] == sdui.KindInvoke && act["mutation"] == mutation {
		return true
	}
	for _, c := range n.GetChildren() {
		if walkFindInvoke(c, mutation) {
			return true
		}
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			if walkFindInvoke(c, mutation) {
				return true
			}
		}
	}
	return false
}

// TestExtensionsScreenSurvivesAnUnreachableRepository pins the resilience rule: a
// repository that cannot be reached must not fail the whole screen, so a user can
// still uninstall when the repo is down.
func TestExtensionsScreenSurvivesAnUnreachableRepository(t *testing.T) {
	fake := &fakeQueries{
		installedExtensions: []app.InstalledExtension{{ModuleID: "stremio", Version: "v0.24.0"}},
		availableErr:        contracts.NewError(contracts.Unavailable, "repository down"),
	}
	node := render(t, &Service{content: fake}, "extensions", nil)

	if _, ok := findButton(node, "Uninstall"); !ok {
		t.Fatal("Uninstall must remain available when the repository is unreachable")
	}
	if _, ok := find(node, sdui.TypeEmptyState); !ok {
		t.Fatal("an unreachable repository should render an empty state for the available list")
	}
}

func TestDetailScreenRequiresNodeId(t *testing.T) {
	_, err := (&Service{content: &fakeQueries{}}).Render(context.Background(), "detail", v1.CallerFromSession("s-1"), nil)
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("category = %s, want InvalidArgument", got)
	}
}

// TestDetailPlayAffordanceIsGatedOnAPartExisting is ADR 0036's rule made
// executable on the emit-side: an affordance must never appear with nothing
// behind it. Being in the library is not enough — a Work has no bytes of its
// own, and a metadata-only import has none anywhere in its tree, so Play is
// offered on the presence of a Part and on nothing else.
func TestDetailPlayAffordanceIsGatedOnAPartExisting(t *testing.T) {
	inLibrary := func(part v1.Part) *fakeQueries {
		return &fakeQueries{
			previewMeta:      v1.ContentMetadata{Title: "Avatar", Year: 2009},
			previewInLibrary: true,
			previewNodeID:    v1.NodeID("work-1"),
			playablePart:     part,
		}
	}

	// No Part anywhere in the tree: In library, and no Play.
	node := render(t, &Service{content: inLibrary(v1.Part{})}, "detail",
		map[string]any{"ref": map[string]any{"provider": "stremio", "nativeId": "tt0499549", "nativeType": "movie"}})
	if btn, ok := findButton(node, "Play"); ok {
		t.Fatalf("Play offered with no playable part: %+v", btn)
	}

	// A Part exists: Play appears, and carries that part's id.
	withPart := v1.Part{ID: v1.PartID("part-7"), Role: v1.PartEdition}
	node = render(t, &Service{content: inLibrary(withPart)}, "detail",
		map[string]any{"ref": map[string]any{"provider": "stremio", "nativeId": "tt0499549", "nativeType": "movie"}})
	btn, ok := findButton(node, "Play")
	if !ok {
		t.Fatal("a library item with a part must offer Play")
	}
	act := actionOf(btn)
	if act["kind"] != sdui.KindInvoke || act["mutation"] != "playPart" {
		t.Fatalf("Play action = %+v, want an Invoke of playPart", act)
	}
	input, _ := act["input"].(map[string]any)
	if input["partId"] != string(withPart.ID) {
		t.Fatalf("Play carried partId %v, want %q", input["partId"], withPart.ID)
	}
}
