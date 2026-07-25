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
// SearchAvailableContent and renders each result as a card (ADR 0028).
func (s *Service) searchScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	text := strings.TrimSpace(stringParam(params, paramText))
	field := s.searchField(text)

	if text == "" {
		return ui.Screen(ui.Title("Search"), ui.Group(field,
			ui.EmptyState(emptyIconSearch, "Find movies, shows and more"))).Build(), nil
	}

	// Ask for one more than the page needs. That extra result is the *only*
	// honest evidence there is another page: a page that happens to be full says
	// nothing, and a client inferring "more" from a full count asks for a page
	// that does not exist. The extra is never rendered — it is a question, not a
	// result.
	page := intParam(params, paramPage)
	if page < 0 {
		page = 0
	}
	want := (page + 1) * searchPageSize
	res, err := s.content.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{
		Caller: caller, Text: text, Limit: want + 1,
	})
	if err != nil {
		return nil, err
	}
	hasMore := len(res.Results) > want
	if hasMore {
		res.Results = res.Results[:want]
	}
	if len(res.Results) == 0 {
		return ui.Screen(ui.Title("Search"), ui.Group(field,
			ui.EmptyState(emptyIconSearch, "No results for \""+text+"\""))).Build(), nil
	}

	// Group the results by media type, as the mockups section them: films, then
	// series, then whatever else the providers returned. A flat grid of mixed
	// kinds makes the viewer do the sorting, and the type is already on every
	// ref — the screen was throwing away a distinction it had been handed.
	//
	// Insertion-ordered rather than alphabetical: the providers rank their own
	// results, and the type of the best match is the one a viewer most likely
	// wants at the top. A single-type result set renders as one section, which is
	// the flat grid this replaced.
	byType := map[v1.MediaType][]ui.El{}
	var order []v1.MediaType
	for _, r := range res.Results {
		if _, seen := byType[r.Ref.MediaType]; !seen {
			order = append(order, r.Ref.MediaType)
		}
		byType[r.Ref.MediaType] = append(byType[r.Ref.MediaType],
			s.contentCard(r.Ref, r.Title, r.Year, r.Poster, r.InLibrary))
	}
	sections := make([]ui.El, 0, len(order))
	for _, mt := range order {
		cards := byType[mt]
		sections = append(sections, ui.Section(mediaTypeHeading(mt),
			ui.Subtitle(strconv.Itoa(len(cards))),
			ui.Grid(ui.Group(cards...))))
	}

	grid := []ui.El{ui.Group(sections...)}
	if hasMore {
		// `query` rather than `navigate`: a further page is not somewhere the
		// back button should return to. A viewer who scrolled through five pages
		// should get one press back to where they came from, not five.
		grid = append(grid,
			ui.HasMore(true),
			ui.LoadMore(ui.Query(screenSearch, map[string]any{
				paramText: text, paramPage: page + 1,
			})))
	}
	return ui.Screen(ui.Title("Search"), ui.Stack("vertical", 7, append([]ui.El{field}, grid...)...)).Build(), nil
}

// mediaTypeHeading is a media type as a section heading. The vocabulary is open
// text canonicalised on write (ADR 0015), so an unknown type is title-cased and
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
	// Canonicalised types are snake_case (ADR 0015), so "manga_series" reads as
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
