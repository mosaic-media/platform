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

	res, err := s.content.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{Caller: caller, Text: text})
	if err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		return ui.Screen(ui.Title("Search"), ui.Group(field,
			ui.EmptyState(emptyIconSearch, "No results for \""+text+"\""))).Build(), nil
	}

	cards := make([]ui.El, 0, len(res.Results))
	for _, r := range res.Results {
		cards = append(cards, s.contentCard(r.Ref, r.Title, r.Year, r.Poster, r.InLibrary))
	}
	return ui.Screen(ui.Title("Search"), ui.Group(field, ui.Grid(cards...))).Build(), nil
}

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
