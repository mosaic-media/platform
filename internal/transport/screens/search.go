// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"strconv"
	"strings"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// searchScreen is the results surface. On desktop the always-present top-bar
// search holds the query and this screen re-renders below it; on mobile it is a
// tab-bar destination with its OWN search field (the top bar has none), the
// native pattern. The field is desktop-hidden and carries a stable id so it
// keeps focus/value across the search-as-you-type re-renders. It runs
// SearchAvailableContent and renders each result as a card (platform#18).
func (s *Service) searchScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	text := strings.TrimSpace(stringParam(params, paramText))
	field := s.searchField(text)

	if text == "" {
		return ui.Screen(ui.Title("Search"), ui.Group(field,
			ui.EmptyState(emptyIconSearch, "Find movies, shows and more"))).Build(), nil
	}

	if focus := v1.MediaType(stringParam(params, paramMediaType)); focus != "" {
		return s.focusedSearch(ctx, caller, text, focus, params, field)
	}
	return s.searchOverview(ctx, caller, text, field)
}

// searchOverview is the unfocused result set: one row per media type, capped,
// each headed by the type and enterable from that heading.
//
// The cap is the point. An unfocused search used to render every result of every
// type as a wrapping grid, so a viewer who knew they wanted a series scrolled
// past forty films to reach one — and the further pages made that worse, because
// paging a mixed list deepens whichever type already dominated. A row per type
// answers "what is there" in a screenful; entering a type answers "show me all
// of them", and that is the list worth paging.
//
// It asks for more than it renders so a row can be filled for the *quieter*
// types too: the providers rank by relevance, not by type, and a page-sized
// sample of a popular film title is all films.
func (s *Service) searchOverview(ctx context.Context, caller v1.Caller, text string, field ui.El) (sdui.Node, error) {
	res, err := s.content.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{
		Caller: caller, Text: text, Limit: searchOverviewSample,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		return ui.Screen(ui.Title("Search"), ui.Group(field,
			ui.EmptyState(emptyIconSearch, "No results for \""+text+"\""))).Build(), nil
	}

	// Insertion-ordered rather than alphabetical: the providers rank their own
	// results, and the type of the best match is the one a viewer most likely
	// wants at the top.
	byType := map[v1.MediaType][]v1.SearchResult{}
	var order []v1.MediaType
	for _, r := range res.Results {
		if _, seen := byType[r.Ref.MediaType]; !seen {
			order = append(order, r.Ref.MediaType)
		}
		byType[r.Ref.MediaType] = append(byType[r.Ref.MediaType], r)
	}

	sections := make([]ui.El, 0, len(order)+1)
	sections = append(sections, s.searchFilterBar(text, order, ""))
	for _, mt := range order {
		hits := byType[mt]
		capped := hits
		if len(capped) > searchRowSize {
			capped = capped[:searchRowSize]
		}
		cards := make([]ui.El, 0, len(capped))
		for _, r := range capped {
			cards = append(cards, s.contentCard(r.Ref, r.Title, r.Year, r.Poster, r.InLibrary))
		}
		// The count is what this sample found, and it is deliberately not
		// presented as a total: the sample is capped, so a type with more than
		// fits is reported as "N+" rather than as a number that would be wrong.
		count := strconv.Itoa(len(hits))
		if len(hits) >= searchOverviewSample {
			count += "+"
		}
		sections = append(sections, ui.Section(mediaTypeHeading(mt),
			ui.Compact(true),
			ui.Subtitle(count),
			ui.Gap(4),
			ui.ActionLabel("See all"),
			ui.PinAction(true),
			ui.OnTap(ui.Navigate(screenSearch, map[string]any{
				paramText: text, paramMediaType: string(mt),
			})),
			// A row rather than a grid: one line that scrolls sideways, so a
			// type never pushes the next one off the screen.
			ui.Carousel(ui.Group(cards...))))
	}
	return ui.Screen(ui.Title("Search"),
		ui.Stack("vertical", 7, append([]ui.El{field}, sections...)...)).Build(), nil
}

// focusedSearch is one media type, in full, paged.
//
// This is where lazy loading belongs and where it now is. The query carries the
// type down to the providers rather than filtering after the fact, so a page is
// a page *of this type* — paging a mixed list to find more series is what the
// overview's cap exists to stop.
func (s *Service) focusedSearch(ctx context.Context, caller v1.Caller, text string,
	focus v1.MediaType, params map[string]any, field ui.El) (sdui.Node, error) {

	// Ask for one more than the page needs. That extra result is the *only*
	// honest evidence there is another page (contracts#16): a page that happens to be
	// full says nothing, and a client inferring "more" from a full count asks for
	// a page that does not exist. The extra is never rendered.
	page := intParam(params, paramPage)
	if page < 0 {
		page = 0
	}
	want := (page + 1) * searchPageSize
	res, err := s.content.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{
		Caller: caller, Text: text, MediaType: focus, Limit: want + 1,
	})
	if err != nil {
		return nil, err
	}
	hasMore := len(res.Results) > want
	if hasMore {
		res.Results = res.Results[:want]
	}

	heading := mediaTypeHeading(focus)
	bar := s.searchFilterBar(text, nil, focus)
	if len(res.Results) == 0 {
		return ui.Screen(ui.Title("Search"), ui.Stack("vertical", 7, field, bar,
			ui.EmptyState(emptyIconSearch, "No "+strings.ToLower(heading)+" for \""+text+"\""))).Build(), nil
	}

	cards := make([]ui.El, 0, len(res.Results))
	for _, r := range res.Results {
		cards = append(cards, s.contentCard(r.Ref, r.Title, r.Year, r.Poster, r.InLibrary))
	}
	grid := []ui.El{ui.Group(cards...)}
	if hasMore {
		// `query` rather than `navigate`: a further page is not somewhere the
		// back button should return to. A viewer who scrolled through five pages
		// should get one press back to where they came from, not five.
		grid = append(grid,
			ui.HasMore(true),
			ui.LoadMore(ui.Query(screenSearch, map[string]any{
				paramText: text, paramMediaType: string(focus), paramPage: page + 1,
			})))
	}
	return ui.Screen(ui.Title("Search"), ui.Stack("vertical", 7, field, bar,
		ui.Section(heading, ui.Compact(true), ui.Gap(4), ui.Grid(grid...)))).Build(), nil
}

// searchFilterBar is the design's row of type chips: All, then one per type.
//
// On the overview it is built from the types this search actually returned,
// because a chip for a type with no results is a control that answers nothing.
// On a focused view the types are not known — the query was filtered — so the
// bar carries All and the focused chip, which is enough to get back out.
func (s *Service) searchFilterBar(text string, present []v1.MediaType, active v1.MediaType) ui.El {
	chip := func(label string, mt v1.MediaType) ui.El {
		variant := "chip"
		if mt == active {
			variant = "chipOn"
		}
		params := map[string]any{paramText: text}
		if mt != "" {
			params[paramMediaType] = string(mt)
		}
		return ui.Button(label, variant, ui.OnTap(ui.Navigate(screenSearch, params)))
	}
	chips := []ui.El{chip("All", "")}
	for _, mt := range present {
		chips = append(chips, chip(mediaTypeHeading(mt), mt))
	}
	if active != "" {
		chips = append(chips, chip(mediaTypeHeading(active), active))
	}
	return ui.Stack("horizontal", 2, ui.Wrap(true), ui.Group(chips...))
}

// mediaTypeHeading is a media type as a section heading. The vocabulary is open
// text canonicalised on write (platform#11), so an unknown type is title-cased and
// shown rather than dropped: a module contributing a type the Platform has never
// heard of should get a heading, not have its results disappear into a section
// with no name.
func mediaTypeHeading(mt v1.MediaType) string {
	switch mt {
	case v1.MediaMovie:
		return "Films"
	case v1.MediaTVSeries:
		return "Series"
	case v1.MediaAnimeSeries:
		return "Anime"
	case v1.MediaAlbum:
		return "Music"
	case v1.MediaIPTVChannel:
		return "Live"
	}
	// Canonicalised types are snake_case (platform#11), so "manga_series" reads as
	// "Manga series" rather than as an identifier.
	name := strings.ReplaceAll(string(mt), "_", " ")
	if name == "" {
		return "Other"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// searchPageSize is how many results a page of search carries.
//
// It is the Platform's number rather than the client's, because the client
// cannot know what an upstream costs: every result here is a row the providers
// were fanned out for and the library was checked against, and the page size is
// the only lever on that fan-out.
const searchPageSize = 24

// searchRowSize is how many results of one type the unfocused search shows.
// One line, which is the whole point: enough to recognise whether what you want
// is in there, and not so many that the next type is off the screen.
const searchRowSize = 12

// searchOverviewSample is how deep the unfocused search looks before capping.
//
// Larger than a page because the providers rank by relevance and not by type: a
// page-sized sample of a popular film title is all films, and the series row
// would be empty for want of looking further rather than for want of series.
const searchOverviewSample = 96

// searchField is the search screen's own input — shown on mobile (where search
// is a tab and the top bar has no field), hidden on desktop, where the
// always-present top-bar field already holds the query. Its stable id lets React
// keep it focused as the results below re-render.
//
// It says that in the payload, through `hidden` + `responsive`, rather than
// through the `kind` hook it used to carry. A `kind` needs a matching rule in
// the client's stylesheet, which puts a client release in the path of a layout
// decision — the thing `responsive` exists to avoid, and why `kind` is
// deprecated in the contract.
func (s *Service) searchField(text string) ui.El {
	return ui.Component("Box",
		ui.Prop("style", map[string]any{
			"hidden": true, "pb": 3,
			"responsive": map[string]any{
				"below": 720,
				"style": map[string]any{"hidden": false},
			},
		}),
		ui.Component("SearchBar",
			ui.ID("search-field"),
			ui.Prop("placeholder", "Find movies, shows and more"),
			ui.When(text != "", ui.Prop("value", text)),
		),
	)
}
