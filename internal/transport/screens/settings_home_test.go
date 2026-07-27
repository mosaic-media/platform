// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"strings"
	"testing"

	sdui "github.com/mosaic-media/contracts/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings › Preferences › Home (ADR 0103) — the panel a viewer arranges their
// own home screen from.

func homePanel(t *testing.T, fake *fakeQueries, session string) sdui.Node {
	t.Helper()
	node, err := (&Service{content: fake}).Render(context.Background(), "settings",
		v1.CallerFromSession(session), map[string]any{paramSection: sectionHomeRows})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return node
}

func homePanelFake() *fakeQueries {
	return &fakeQueries{
		catalogs: []app.ModuleCatalog{
			{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "trending", NativeType: "movie", Name: "Trending Films"}},
			{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "trending", NativeType: "tv", Name: "Trending Series"}},
			{ModuleID: "tmdb", Catalog: v1.Catalog{ID: "popular", NativeType: "movie", Name: "Popular Films"}},
		},
		compositions: map[string]app.HomeComposition{},
	}
}

func rowLabels(node sdui.Node) []string {
	var rows []sdui.Node
	findAll(node, sdui.TypeSettingsRow, &rows)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		label, _ := prop(r, "label").(string)
		out = append(out, label)
	}
	return out
}

// TestHomePanelNamesEveryRowOnce is a regression, and the reason it exists is
// worth more than the assertion.
//
// The panel drew every row from one shared key list, and building a row's move
// control reordered that list in place — so every row after the first was
// labelled with the row above it and one catalog vanished entirely. Every unit
// test passed. It was wrong on sight the moment the screen was opened in a
// browser, which is the only place it could have been seen.
func TestHomePanelNamesEveryRowOnce(t *testing.T) {
	got := rowLabels(homePanel(t, homePanelFake(), "s-1"))
	want := []string{"Continue watching", "Trending Films", "Trending Series", "Popular Films"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

// TestHomePanelOffersAHiddenRowBack is the difference between the panel and the
// screen: home drops a hidden row, and the panel must keep it in place with its
// switch off, or the only way to restore one is to remember it existed.
func TestHomePanelOffersAHiddenRowBack(t *testing.T) {
	fake := homePanelFake()
	fake.compositions["hider"] = app.HomeComposition{Hidden: []string{"catalog:tmdb:tv:trending"}}
	node := homePanel(t, fake, "hider")

	if got := rowLabels(node); len(got) != 4 {
		t.Fatalf("rows = %v, want the hidden row still listed", got)
	}
	var toggles []sdui.Node
	findAll(node, sdui.TypeToggle, &toggles)
	off := 0
	for _, tg := range toggles {
		if on, _ := prop(tg, "on").(bool); !on {
			off++
		}
	}
	if off != 1 {
		t.Fatalf("switches off = %d, want exactly the hidden row's", off)
	}
}

// TestHomePanelControlsCarryTheDecisionTheyMake proves the client echoes rather
// than authors: every control's action is a setPreference whose value is the
// document that control produces, computed here where the row list is in hand.
func TestHomePanelControlsCarryTheDecisionTheyMake(t *testing.T) {
	node := homePanel(t, homePanelFake(), "s-1")

	var buttons []sdui.Node
	findAll(node, sdui.TypeButton, &buttons)
	var moves int
	for _, b := range buttons {
		label, _ := prop(b, "label").(string)
		if label != "Up" && label != "Down" {
			continue
		}
		moves++
		action, _ := prop(b, "action").(map[string]any)
		if disabled, _ := prop(b, "disabled").(bool); disabled {
			if action != nil {
				t.Fatal("a move that cannot happen must carry no action")
			}
			continue
		}
		if action["mutation"] != setPreferenceMutation {
			t.Fatalf("action = %v, want a setPreference", action)
		}
		input, _ := action["input"].(map[string]any)
		if input["key"] != domain.PreferenceHomeRows {
			t.Fatalf("input = %v, want the home-rows preference", input)
		}
		value, _ := input["value"].(map[string]any)
		if _, ok := value["order"]; !ok {
			t.Fatalf("value = %v, want the arrangement the press would produce", value)
		}
	}
	// Four rows, two controls each: the first row cannot move up and the last
	// cannot move down, and both are drawn disabled rather than left out.
	if moves != 8 {
		t.Fatalf("move controls = %d, want two per row", moves)
	}
}
