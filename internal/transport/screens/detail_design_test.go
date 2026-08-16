// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"testing"

	sdui "github.com/mosaic-media/contracts/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// sectionOrder is the titled bands of a screen, in the order they are emitted.
// Order is the assertion here, not membership: the design states a sequence and
// a set-based check would pass with the episodes underneath the cast.
func sectionOrder(n sdui.Node) []string {
	var sections []sdui.Node
	findAll(n, sdui.TypeSection, &sections)
	out := make([]string, 0, len(sections))
	for _, sec := range sections {
		if s, _ := prop(sec, "title").(string); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// TestSeriesDetailPutsEpisodesBeforeCast pins that a series' episodes come
// before its cast. Someone opening a show they are part-way through wants the
// next episode, and a rail of headshots between them and it makes the common
// case scroll past the rare one.
func TestSeriesDetailPutsEpisodesBeforeCast(t *testing.T) {
	ref := v1.ContentRef{Provider: "tmdb", NativeID: "1396", NativeType: "series", MediaType: v1.MediaTVSeries}
	fake := &fakeQueries{
		previewMeta: v1.ContentMetadata{
			Title: "A Show",
			Cast:  []v1.Person{{Name: "Someone", Role: "A Part"}},
			Episodes: []v1.EpisodePreview{
				{Season: 1, Episode: 1, Title: "One"},
				{Season: 1, Episode: 2, Title: "Two"},
			},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(ref)})

	order := sectionOrder(node)
	var episodes, cast = -1, -1
	for i, s := range order {
		switch s {
		case "Episodes":
			episodes = i
		case "Cast":
			cast = i
		}
	}
	if episodes < 0 || cast < 0 {
		t.Fatalf("sections = %v, want both Episodes and Cast", order)
	}
	if episodes > cast {
		t.Errorf("sections = %v, want Episodes before Cast", order)
	}
}

// TestSeriesDetailKickerCarriesTheSeasonCount pins that the eyebrow says what
// the thing is and how much of it there is. The season count lives there rather
// than among the meta pills, where it read as one more attribute beside a
// rating and a certificate.
func TestSeriesDetailKickerCarriesTheSeasonCount(t *testing.T) {
	ref := v1.ContentRef{Provider: "tmdb", NativeID: "1396", NativeType: "series", MediaType: v1.MediaTVSeries}
	fake := &fakeQueries{
		previewMeta: v1.ContentMetadata{
			Title: "A Show",
			Episodes: []v1.EpisodePreview{
				{Season: 1, Episode: 1, Title: "One"},
				{Season: 2, Episode: 1, Title: "Two"},
			},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(ref)})
	hero, ok := find(node, "DetailHero")
	if !ok {
		t.Fatal("no DetailHero")
	}
	if got, _ := prop(hero, "kicker").(string); got != "Series · 2 seasons" {
		t.Errorf("kicker = %q, want %q", got, "Series · 2 seasons")
	}
}

// TestDetailHeroCarriesTheCrewLine pins that the crew line, from credits that
// were arriving and being discarded. Names are grouped by job so a show with
// two creators reads as one phrase.
func TestDetailHeroCarriesTheCrewLine(t *testing.T) {
	ref := v1.ContentRef{Provider: "tmdb", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}
	fake := &fakeQueries{
		previewMeta: v1.ContentMetadata{
			Title: "A Film",
			Crew: []v1.Person{
				{Name: "A Person", Role: "Creator"},
				{Name: "B Person", Role: "Creator"},
				{Name: "C Person", Role: "Director"},
			},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(ref)})
	hero, ok := find(node, "DetailHero")
	if !ok {
		t.Fatal("no DetailHero")
	}
	want := "Created by A Person and B Person · Directed by C Person"
	if got, _ := prop(hero, "credits").(string); got != want {
		t.Errorf("credits = %q, want %q", got, want)
	}
}

// TestEpisodeRowsCarryRuntimeQualityAndProgress pins that an episode row's
// facts column carries the runtime and the release quality — the two the design
// puts there. Both were reachable and neither was read: the runtime arrives on
// every episode of every season, and the quality is on the episode's own Part.
func TestEpisodeRowsCarryRuntimeQualityAndProgress(t *testing.T) {
	ref := v1.ContentRef{Provider: "tmdb", NativeID: "1396", NativeType: "series", MediaType: v1.MediaTVSeries}
	fake := &fakeQueries{
		previewInLibrary: true,
		previewNodeID:    "series-1",
		previewMeta: v1.ContentMetadata{
			Title: "A Show",
			Episodes: []v1.EpisodePreview{
				{Season: 1, Episode: 1, Title: "One", RuntimeMinutes: 51},
				{Season: 1, Episode: 2, Title: "Two", RuntimeMinutes: 46},
			},
		},
		playablePart: v1.Part{ID: "p-1", NodeID: "ep-1", Height: 2160, HDRFormat: "HDR10"},
		childrenByNode: map[v1.NodeID][]v1.Node{
			"series-1": {{ID: "season-1", Kind: v1.NodeContainer, NaturalOrder: 1}},
			"season-1": {
				{ID: "ep-1", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 1},
				{ID: "ep-2", Kind: v1.NodeItem, ItemType: v1.ItemEpisode, NaturalOrder: 2},
			},
		},
		playbackStates: map[v1.NodeID]v1.PlaybackState{
			// Half-way through the first, and the second untouched.
			"ep-1": {Position: 1500 * 1e9, Duration: 3000 * 1e9},
		},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(ref)})

	var rows []sdui.Node
	findAll(node, "EpisodeRow", &rows)
	if len(rows) != 2 {
		t.Fatalf("episode rows = %d, want 2", len(rows))
	}
	if got, _ := prop(rows[0], "index").(string); got != "S1 E1" {
		t.Errorf("index = %q, want %q — the season is half the answer", got, "S1 E1")
	}
	if got, _ := prop(rows[0], "runtime").(string); got != "51 min" {
		t.Errorf("runtime = %q, want %q", got, "51 min")
	}
	if got, _ := prop(rows[0], "quality").(string); got != "4K HDR" {
		t.Errorf("quality = %q, want %q", got, "4K HDR")
	}
	if got, _ := prop(rows[0], "progress").(float64); got != 0.5 {
		t.Errorf("progress = %v, want 0.5", got)
	}
	// A row nobody has started carries no bar rather than a zero-length one.
	if _, present := prop(rows[1], "progress").(float64); present {
		t.Error("an unstarted episode must carry no progress at all")
	}
}

// TestFactsGridOmitsReleaseCardsForAVirtualItem pins that the facts grid states
// what the release is, and states nothing where Mosaic has no answer. A virtual
// item has no bytes to describe, so only the metadata card is drawn — four
// empty cards are worse than none.
func TestFactsGridOmitsReleaseCardsForAVirtualItem(t *testing.T) {
	ref := v1.ContentRef{Provider: "tmdb", NativeID: "tt1", NativeType: "movie", MediaType: v1.MediaMovie}
	fake := &fakeQueries{
		previewMeta:     v1.ContentMetadata{Title: "Not Here Yet"},
		settingsModules: []app.SettingsModule{{ModuleID: "tmdb", Name: "TMDB"}},
	}
	node := render(t, &Service{content: fake}, "detail", map[string]any{paramRef: refInput(ref)})

	var cards []sdui.Node
	findAll(node, "FactCard", &cards)
	if len(cards) != 1 {
		t.Fatalf("fact cards = %d, want 1 (metadata only) for a virtual item", len(cards))
	}
	if got, _ := prop(cards[0], "label").(string); got != "Metadata" {
		t.Errorf("card label = %q, want Metadata", got)
	}
	// Attributed to the module's own name for itself rather than to its id.
	lines, _ := prop(cards[0], "lines").([]any)
	if len(lines) == 0 || lines[0] != "TMDB matched" {
		t.Errorf("metadata lines = %v, want the module's display name", lines)
	}
}
