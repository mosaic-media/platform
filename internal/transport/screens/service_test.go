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
	results      []v1.SearchResult
	catalogs     []app.ModuleCatalog
	items        []v1.CatalogItem
	node         v1.Node
	children     []v1.Node
	gotText      string
	gotCatalogID string
	gotNodeID    v1.NodeID
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

	// The virtual card materialises via importContent with the ref round-tripped.
	virtual := cards[0]
	act, _ := virtual.Props["action"].(sdui.Action)
	if act.Kind != sdui.KindInvoke || act.Mutation == nil || *act.Mutation != "importContent" {
		t.Fatalf("virtual card action = %+v, want Invoke importContent", act)
	}
	ref, _ := act.Input["ref"].(map[string]any)
	if ref["externalId"] != "tt1254207" || ref["provider"] != "stremio" {
		t.Fatalf("materialise ref = %+v, want the result's ref", ref)
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

func TestCatalogScreenRendersItemsWithMaterialiseAction(t *testing.T) {
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
	if act.Kind != sdui.KindInvoke || act.Mutation == nil || *act.Mutation != "importContent" {
		t.Fatalf("catalog item action = %+v, want Invoke importContent", act)
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

func TestDetailScreenRequiresNodeId(t *testing.T) {
	_, err := (&Service{content: &fakeQueries{}}).Render(context.Background(), "detail", v1.CallerFromSession("s-1"), nil)
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("category = %s, want InvalidArgument", got)
	}
}
