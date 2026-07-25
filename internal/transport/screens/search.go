// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
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
				paramText: text, paramPage: page + 1,
			})))
	}
	return ui.Screen(ui.Title("Search"), ui.Group(field, ui.Grid(grid...))).Build(), nil
}

// searchPageSize is how many results a page of search carries.
//
// It is the Platform's number rather than the client's, because the client
// cannot know what an upstream costs: every result here is a row the providers
// were fanned out for and the library was checked against, and the page size is
// the only lever on that fan-out.
const searchPageSize = 24

// searchField is the search screen's own input — shown on mobile (where search
// is a tab and the top bar has no field), hidden on desktop (data-kind). Its
// stable id lets React keep it focused as the results below re-render.
func (s *Service) searchField(text string) ui.El {
	return ui.Component("Box",
		ui.Prop("style", map[string]any{"kind": "screen-search", "pb": 3}),
		ui.Component("SearchBar",
			ui.ID("search-field"),
			ui.Prop("placeholder", "Find movies, shows and more"),
			ui.When(text != "", ui.Prop("value", text)),
		),
	)
}
