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
	results []v1.SearchResult
	gotText string
}

func (f *fakeQueries) SearchAvailableContent(_ context.Context, q app.SearchAvailableContentQuery) (app.SearchAvailableContentResult, error) {
	f.gotText = q.Text
	return app.SearchAvailableContentResult{Results: f.results}, nil
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
