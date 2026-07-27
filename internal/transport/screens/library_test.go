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
	if !strings.Contains(text, "showing 1–60") {
		t.Errorf("the screen did not say which slice is on it; screen said: %s", text)
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

// Paging is the interaction the library has and a provider catalog does not:
// a total to page against rather than an ask-for-more.
func TestTheLibraryPages(t *testing.T) {
	fake := libraryService(libraryWorks(140))
	svc := &Service{content: fake}

	first := render(t, svc, "library", nil)
	pager, ok := find(first, "Pagination")
	if !ok {
		t.Fatal("the first page of 140 titles offers no way to the second")
	}
	if prop(pager, "hasPrev") != false || prop(pager, "hasNext") != true {
		t.Fatalf("first page's pager = %v", pager.GetProps().AsMap())
	}
	if got := prop(pager, "label"); got != "Page 1 of 3" {
		t.Fatalf("pager label = %v, want the page and the count of pages", got)
	}

	second := render(t, svc, "library", map[string]any{"page": float64(1)})
	if !strings.Contains(treeStrings(second), "showing 61–120") {
		t.Errorf("the second page did not say where it is: %s", treeStrings(second))
	}

	last := render(t, svc, "library", map[string]any{"page": float64(2)})
	pager, ok = find(last, "Pagination")
	if !ok {
		t.Fatal("the last page has no pager, so there is no way back")
	}
	if prop(pager, "hasNext") != false {
		t.Error("the last page offers a next page")
	}
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

// A bookmark from when the library was larger is an ordinary thing to follow,
// and it must not read as an empty library.
func TestAPagePastTheEndSaysSo(t *testing.T) {
	fake := libraryService(libraryWorks(10))
	node := render(t, &Service{content: fake}, "library", map[string]any{"page": float64(9)})

	text := treeStrings(node)
	if strings.Contains(text, "Nothing in the library yet") {
		t.Error("a page past the end rendered as an empty library")
	}
	if !strings.Contains(text, "10 titles") {
		t.Errorf("the page past the end did not state the real total: %s", text)
	}
	if _, ok := findButton(node, "Back to the first page"); !ok {
		t.Error("no way back from a page that does not exist")
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
