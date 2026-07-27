// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"fmt"
	"strings"
	"testing"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The Library screen (roadmap M2.1) — the first screen over the object graph.
//
// What these assert is what a provider-backed screen cannot do: state a real
// total, page against it, and open a title by the id the install owns rather
// than by a ref that sends the detail back to a source.

func libraryWorks(n int) []v1.Node {
	out := make([]v1.Node, 0, n)
	for i := 0; i < n; i++ {
		id := v1.NodeID(fmt.Sprintf("node-%02d", i))
		out = append(out, v1.Node{
			ID: id, WorkID: id, Kind: v1.NodeWork, MediaType: v1.MediaMovie,
			Title:   fmt.Sprintf("Film %02d", i),
			Artwork: v1.Artwork{Poster: "https://cdn/p" + string(id) + ".jpg"},
		})
	}
	return out
}

func libraryService(works []v1.Node) *fakeQueries {
	return &fakeQueries{libraryWorks: works, allow: map[string]bool{}}
}

func TestTheLibraryScreenStatesARealTotal(t *testing.T) {
	fake := libraryService(libraryWorks(140))
	node := render(t, &Service{content: fake}, "library", nil)

	text := treeStrings(node)
	// A real number and no "+". The catalog screen hedges because a provider
	// will not say how large its catalog is; these are rows the install owns,
	// and hedging a number you can count reads as a system that does not know
	// what it has.
	if !strings.Contains(text, "140 titles") {
		t.Errorf("the library did not state its total; screen said: %s", text)
	}
	if strings.Contains(text, "140+") {
		t.Error("the library hedged a total it can count")
	}
}

// A card must open something the install owns, by node id. A card built from a
// ref would send the detail back to the provider that sourced it, which is the
// one thing a library screen must not depend on.
func TestALibraryCardOpensTheNodeTheInstallOwns(t *testing.T) {
	fake := libraryService(libraryWorks(3))
	node := render(t, &Service{content: fake}, "library", nil)

	var cards []sdui.Node
	findAll(node, "PosterCard", &cards)
	if len(cards) != 3 {
		t.Fatalf("rendered %d cards, want 3", len(cards))
	}
	action := actionOf(cards[0])
	params, _ := action["params"].(map[string]any)
	if params["nodeId"] != "node-00" {
		t.Fatalf("a library card navigates with %v, want the node id it owns", params)
	}
	if _, byRef := params["ref"]; byRef {
		t.Error("a library card carries a provider ref, so its detail depends on a source being up")
	}
}

// The library scrolls lazily rather than paging (ADR 0093): the grid says it is
// a page of something longer and what fetches the rest, and the client asks as
// the end comes into view.
//
// **`hasMore` is computed from the total, not from the page being full.** A full
// page is not evidence of another, and a client that inferred one would ask for
// a page that does not exist — which is the whole reason the server states it.
func TestTheLibraryScrollsLazily(t *testing.T) {
	fake := libraryService(libraryWorks(140))
	svc := &Service{content: fake}

	first := render(t, svc, "library", nil)
	grid, ok := find(first, "Grid")
	if !ok {
		t.Fatal("the library rendered no grid")
	}
	if prop(grid, "hasMore") != true {
		t.Fatalf("a grid of 60 over 140 titles does not offer the rest: %v", grid.GetProps().AsMap())
	}
	load, _ := prop(grid, "loadMore").(map[string]any)
	if load["kind"] != "query" {
		t.Fatalf("loadMore = %v, want a query — a further page must not push a history entry", load)
	}
	params, _ := load["params"].(map[string]any)
	if params["page"] != float64(1) {
		t.Fatalf("loadMore asks for %v, want the next page", params)
	}
	if got := countCards(first); got != 60 {
		t.Fatalf("the first window held %d cards, want 60", got)
	}

	// The next page is the whole window again, not just its tail: a query
	// *replaces* the content region, so the screen must carry everything loaded
	// so far or scrolling would throw away what is above.
	second := render(t, svc, "library", map[string]any{"page": float64(1)})
	if got := countCards(second); got != 120 {
		t.Fatalf("the second window held %d cards, want 120", got)
	}

	// And the end of the library stops asking, which is what keeps the observer
	// from requesting a page that does not exist.
	last := render(t, svc, "library", map[string]any{"page": float64(2)})
	grid, _ = find(last, "Grid")
	if prop(grid, "hasMore") == true {
		t.Error("the end of the library still offers more")
	}
	if got := countCards(last); got != 140 {
		t.Fatalf("the last window held %d cards, want all 140", got)
	}
}

// A lazy list that silently stops loading is indistinguishable from one that
// reached the end — and the count above it would be visibly larger than what is
// on screen, with nothing to explain the gap.
func TestTheLibraryScrollStopsAndSaysSo(t *testing.T) {
	fake := libraryService(libraryWorks(900))
	node := render(t, &Service{content: fake}, "library", map[string]any{"page": float64(20)})

	grid, _ := find(node, "Grid")
	if prop(grid, "hasMore") == true {
		t.Error("the scroll passed its cap and kept asking")
	}
	if got := countCards(node); got != 600 {
		t.Fatalf("the capped window held %d cards, want the cap of 600", got)
	}
	text := treeStrings(node)
	if !strings.Contains(text, "900 titles") {
		t.Errorf("the capped screen stopped stating the real total: %s", text)
	}
	if !strings.Contains(text, "showing the first 600") {
		t.Errorf("the scroll stopped without saying so: %s", text)
	}
}

func countCards(n sdui.Node) int {
	var cards []sdui.Node
	findAll(n, "PosterCard", &cards)
	return len(cards)
}

func TestTheLibraryScreenWhenThereIsNothingInIt(t *testing.T) {
	fake := libraryService(nil)
	node := render(t, &Service{content: fake}, "library", nil)

	text := treeStrings(node)
	if !strings.Contains(text, "Nothing in the library yet") {
		t.Errorf("the empty library said: %s", text)
	}
	// The empty state leads somewhere. An empty state with nothing to press is
	// the dead end ADR 0036 exists to remove, and the library's answer to "how
	// do I fill this" is the rules screen.
	if _, ok := findButton(node, "Library rules"); !ok {
		t.Error("the empty library does not offer the rules that would fill it")
	}
}

// A link into a library that has since shrunk must not read as an empty
// library. The two states say different things because they are different
// facts: one is a new install, the other is a stale link, and telling somebody
// with an empty library to go back to the start would be nonsense.
func TestAWindowThatCameBackEmptyIsNotAnEmptyLibrary(t *testing.T) {
	fake := libraryService(nil)
	fake.libraryTotal = 10
	node := render(t, &Service{content: fake}, "library", nil)

	text := treeStrings(node)
	if strings.Contains(text, "Nothing in the library yet") {
		t.Error("a window that came back empty rendered as an empty library")
	}
	if !strings.Contains(text, "10 titles") {
		t.Errorf("it stopped stating the real total: %s", text)
	}
	if _, ok := findButton(node, "Back to the start"); !ok {
		t.Error("no way back")
	}
}

// The nav row is the whole reason this screen is reachable. Without it the
// screen exists and nobody finds it, which is the shape of the register's
// entries rather than of a shipped feature.
func TestTheShellOffersTheLibrary(t *testing.T) {
	fake := &fakeQueries{currentUser: domain.User{ID: "u-1", Username: "sam"}, allow: map[string]bool{}}
	node := render(t, &Service{content: fake}, "shell", map[string]any{"screen": "home"})

	var items []sdui.Node
	findAll(node, "NavItem", &items)
	var found bool
	for _, item := range items {
		if prop(item, "screen") == "library" {
			found = true
		}
	}
	if !found {
		t.Error("the nav has no way to the library")
	}
}

// Guards the count sentence itself, because it is the one string on this screen
// somebody reads as a fact about their own server.
func TestLibraryCountLabel(t *testing.T) {
	for _, tc := range []struct {
		total int
		want  string
	}{{0, "0 titles"}, {1, "1 title"}, {2, "2 titles"}} {
		if got := libraryCountLabel(tc.total); got != tc.want {
			t.Errorf("libraryCountLabel(%d) = %q, want %q", tc.total, got, tc.want)
		}
	}
}

// A long-running series is read one season at a time (ADR 0107), and the screen
// must still say what the *series* is rather than what the read was.
//
// This is the defect the worst case in a real library exposed: a programme with
// seventy-five seasons announced itself as "Series · 1 season" over a selector
// offering all seventy-five, because the count came from the episodes on hand.
func TestASeriesCountsItsSeasonsNotTheOneItLoaded(t *testing.T) {
	children := make([]v1.Node, 0, 40)
	for n := 1; n <= 40; n++ {
		children = append(children, v1.Node{
			ID: v1.NodeID(fmt.Sprintf("s-%02d", n)), ParentID: nodeRef("n-1"),
			Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason,
			MediaType: v1.MediaTVSeries, Title: fmt.Sprintf("Season %d", n),
			NaturalOrder: float64(n),
		})
	}
	fake := &fakeQueries{
		allow: map[string]bool{},
		node: v1.Node{
			ID: "n-1", WorkID: "n-1", Kind: v1.NodeWork,
			MediaType: v1.MediaTVSeries, Title: "Tagesschau",
		},
		hasStoredMetadata: true,
		storedMetadata: v1.ContentMetadata{
			Ref:   v1.ContentRef{Provider: "tmdb", NativeID: "94722", NativeType: "tv", MediaType: v1.MediaTVSeries},
			Title: "Tagesschau", Year: 1952,
		},
		libraryChildren: children,
		libraryEpisodes: map[int][]v1.Node{
			1: {{ID: "e-1", ParentID: nodeRef("s-01"), Kind: v1.NodeItem,
				ItemType: v1.ItemEpisode, Title: "One", NaturalOrder: 1}},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{"nodeId": "n-1"})

	hero, ok := find(node, "DetailHero")
	if !ok {
		t.Fatal("no hero")
	}
	if got, _ := prop(hero, "kicker").(string); !strings.Contains(got, "40 seasons") {
		t.Errorf("kicker = %q, want the series 40 seasons and not the one season read", got)
	}
	// And the panel states the seasons rather than a count of the episodes it
	// happens to hold.
	if !strings.Contains(treeStrings(node), "Seasons") {
		t.Errorf("the about panel does not state the season count: %s", treeStrings(node))
	}
}
