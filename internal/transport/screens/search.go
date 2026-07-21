// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"strings"

	sdui "github.com/mosaic-media/mosaic-sdui/sdui"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// searchScreen is the results surface that takes over the content region while a
// user types in the always-present top-bar search (ADR 0032). It carries no
// search bar of its own — the top bar holds the query. It runs
// SearchAvailableContent and renders each result as a card (ADR 0028's union of
// in-library and virtual candidates). An empty query does not reach here: the
// live session re-renders the current screen instead of an empty search.
func (s *Service) searchScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	text := strings.TrimSpace(stringParam(params, paramText))
	if text == "" {
		return emptyScreen("Search", emptyIconSearch, "Type to search for something to add"), nil
	}

	res, err := s.content.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{Caller: caller, Text: text})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(res.Results) == 0 {
		return emptyScreen("Search", emptyIconSearch, "No results for \""+text+"\""), nil
	}

	cards := make([]sdui.Node, 0, len(res.Results))
	for _, r := range res.Results {
		cards = append(cards, s.contentCard(r.Ref, r.Title, r.Year, r.Poster, r.InLibrary))
	}
	return gridScreen("Search for \""+text+"\"", cards...), nil
}
