// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"testing"

	sdui "github.com/mosaic-media/contracts/sdui"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

func manyResults(n int, mt v1.MediaType, prefix string) []v1.SearchResult {
	out := make([]v1.SearchResult, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, v1.SearchResult{
			Ref:   v1.ContentRef{Provider: "p", NativeID: prefix + string(rune('a'+i%26)), NativeType: "x", MediaType: mt},
			Title: prefix,
		})
	}
	return out
}

// TestSearchOverviewCapsEachTypeToARow pins that the unfocused search caps each
// type at a row. Without it a viewer who knows they want a series scrolls past
// every film the providers ranked higher.
func TestSearchOverviewCapsEachTypeToARow(t *testing.T) {
	fake := &fakeQueries{results: append(
		manyResults(40, v1.MediaMovie, "film"),
		manyResults(30, v1.MediaTVSeries, "show")...)}
	node := render(t, &Service{content: fake}, "search", map[string]any{"text": "x"})

	var sections []sdui.Node
	findAll(node, sdui.TypeSection, &sections)
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want one per type", len(sections))
	}
	for _, sec := range sections {
		var cards []sdui.Node
		findAll(sec, sdui.TypePosterCard, &cards)
		if len(cards) > searchRowSize {
			t.Errorf("%v renders %d cards, want at most %d", prop(sec, "title"), len(cards), searchRowSize)
		}
		// And the heading is the way in, not a link beside it.
		if prop(sec, "compact") != true {
			t.Errorf("%v is not the compact heading treatment", prop(sec, "title"))
		}
		act, _ := prop(sec, "action").(map[string]any)
		if act["screen"] != "search" || mapAt(act, "params")["mediaType"] == nil {
			t.Errorf("%v heading action = %+v, want a search focused on its type", prop(sec, "title"), act)
		}
	}
	// The overview does not page. Paging a mixed list deepens whichever type
	// already dominated, which is the problem the cap exists to solve.
	if _, ok := prop(node, "hasMore").(bool); ok {
		t.Error("the unfocused search must not page")
	}
}

// TestFocusedSearchFiltersAndPages pins that a focused search is one type, in
// full, and this is where lazy loading lives.
func TestFocusedSearchFiltersAndPages(t *testing.T) {
	fake := &fakeQueries{results: manyResults(searchPageSize+5, v1.MediaTVSeries, "show")}
	node := render(t, &Service{content: fake}, "search",
		map[string]any{"text": "x", "mediaType": string(v1.MediaTVSeries)})

	if fake.gotSearchMediaType != v1.MediaTVSeries {
		t.Fatalf("query media type = %q, want the focused type — filtering after the fact pages the wrong list", fake.gotSearchMediaType)
	}
	grid, ok := find(node, sdui.TypeGrid)
	if !ok {
		t.Fatal("no results grid")
	}
	if prop(grid, "hasMore") != true {
		t.Error("a focused search with more results must say so")
	}
	act, _ := prop(grid, "loadMore").(map[string]any)
	if act["kind"] != sdui.KindQuery {
		t.Errorf("loadMore = %+v, want a query — a further page is not a history entry", act)
	}
	if mapAt(act, "params")["mediaType"] != string(v1.MediaTVSeries) {
		t.Errorf("loadMore params = %+v, want the focus carried into the next page", mapAt(act, "params"))
	}
}
