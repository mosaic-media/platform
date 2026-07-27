// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The Library screen (roadmap M2.1) — the first screen in Mosaic that reads the
// object graph rather than a provider.
//
// Every other browse surface is a window onto somebody else's shelf: home
// renders provider catalogs, `collections` and `catalog` browse a *module's*
// collections, and search unions the library with the sources. Nothing showed
// what the install actually owns, which is why nobody could tell whether the
// library was what they thought it was.
//
// It says two things the catalog screen cannot, and both are consequences of
// reading the graph. The **count is real** — the catalog screen renders "128+"
// because a provider will not say how large its catalog is, and these are the
// install's own rows. And the **titles are the library's own**, read from the
// nodes rather than re-derived from whichever source last answered, so a title
// stays put when a provider is down.

// libraryPageSize is how many works one lazy page adds.
//
// The screen scrolls rather than paging: a `query` action replaces the content
// region, so each further page is the whole window re-rendered, and the client's
// lazy-list observer asks for the next one as the end of the grid comes into
// view (ADR 0093). That is why this is a page *increment* and not a page.
const libraryPageSize = 60

// libraryWindowCap is where the scroll stops.
//
// Every further page re-sends everything above it, so an unbounded scroll over
// a large library is a payload that grows quadratically in the number of pages
// somebody scrolls through. The screen stops offering more here and **says
// so**, because a lazy list that silently stops loading is indistinguishable
// from one that has reached the end — and the count above it would then be
// visibly larger than what is on screen with nothing to explain the gap.
const libraryWindowCap = 600

// libraryScreen renders one page of what the install owns.
func (s *Service) libraryScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	page := intParam(params, paramPage)
	if page < 0 {
		page = 0
	}

	// One read of everything loaded so far, rather than a read per page: the
	// library is the install's own rows and can be windowed directly, where a
	// provider's catalog has to be walked a page at a time.
	window := (page + 1) * libraryPageSize
	if window > libraryWindowCap {
		window = libraryWindowCap
	}
	res, err := s.content.ListLibrary(ctx, app.ListLibraryQuery{
		Caller: caller,
		Limit:  window,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}

	if res.Total == 0 {
		// The genuinely empty library. It is worth a different sentence from
		// "this page is past the end" below, because one is a new install and
		// the other is a stale link, and telling somebody with an empty library
		// to go back a page would be nonsense.
		return ui.Screen(
			ui.Title("Library"),
			ui.Subtitle("Everything this server owns."),
			ui.EmptyState(emptyIconCollections,
				"Nothing in the library yet",
				ui.Message("Add something from search, or set up a library rule in Settings and let the "+
					"server keep it filled."),
				ui.ActionSlot(
					ui.Button("Search for something", "primary", ui.IconName("search"),
						ui.OnTap(ui.Navigate(screenSearch, nil))),
					ui.Button("Library rules", "secondary", ui.IconName("settings"),
						ui.OnTap(ui.Navigate(screenSettings, map[string]any{paramSection: sectionLibraryRules}))),
				),
			),
		).Build(), nil
	}

	if len(res.Works) == 0 {
		// A window that landed past the end — a stale link, or a library that
		// shrank. The count is still true, so the screen says it and offers the
		// way back rather than looking like an empty library.
		return ui.Screen(
			ui.Title("Library"),
			ui.Subtitle(libraryCountLabel(res.Total)),
			ui.EmptyState(emptyIconNotFound,
				"There is nothing here",
				ui.Message("The library has "+libraryCountLabel(res.Total)+"."),
				ui.ActionSlot(ui.Button("Back to the start", "primary",
					ui.OnTap(ui.Navigate(screenLibrary, nil)))),
			),
		).Build(), nil
	}

	cards := make([]ui.El, 0, len(res.Works))
	for _, work := range res.Works {
		cards = append(cards, s.libraryCard(work))
	}

	// The lazy list (ADR 0093). `hasMore` and `loadMore` are the server's
	// statement that this is a page of something longer and what fetches the
	// rest; the client observes the end of the grid coming into view and asks.
	// A full page is deliberately not evidence of another — that is why this is
	// computed from the total rather than from the number of cards.
	grid := []ui.El{ui.MinColumnWidth(196), ui.Group(cards...)}
	more := len(res.Works) < res.Total
	if more && window < libraryWindowCap {
		grid = append(grid, ui.HasMore(true),
			ui.LoadMore(ui.Query(screenLibrary, map[string]any{paramPage: page + 1})))
	}

	return ui.Screen(
		ui.Title("Library"),
		ui.Subtitle(librarySubtitle(res, more, window)),
		ui.Grid(grid...),
	).Build(), nil
}

// librarySubtitle is the live count, and — only when the scroll has stopped
// short of the whole library — what is actually on screen.
//
// While more is still loading it says the total alone: a number that ticks
// upward as somebody scrolls is noise, and the total is the fact they came for.
func librarySubtitle(res app.ListLibraryResult, more bool, window int) string {
	if more && window >= libraryWindowCap {
		return fmt.Sprintf("%s · showing the first %d — search to narrow it down",
			libraryCountLabel(res.Total), len(res.Works))
	}
	return libraryCountLabel(res.Total)
}

// libraryCard is one work as the library holds it.
//
// It opens the detail screen **by node id, not by ref**, and that is the
// difference between this grid and every other one in Mosaic. A card built from
// a ref sends the detail back to the provider that sourced it; a card built from
// a node id opens something the install owns, which still renders if the source
// is unreachable.
func (s *Service) libraryCard(work v1.Node) *ui.Element {
	els := make([]ui.El, 0, 3)
	if poster := work.Artwork.Poster; poster != "" {
		els = append(els, ui.Poster(s.art(poster)))
	}
	els = append(els, ui.OnTap(ui.Navigate(screenDetail, map[string]any{
		paramNodeID: string(work.ID),
	})))
	return ui.PosterCard(work.Title, string(work.MediaType), els...)
}

// libraryCountLabel is the real total, stated plainly.
//
// No "+" anywhere in it, deliberately. The catalog screen's countLabel exists
// because a provider pages its own catalog and will not say how big it is; this
// one counts rows the install owns, and hedging a number you can count reads as
// a system that does not know what it has.
func libraryCountLabel(total int) string {
	if total == 1 {
		return "1 title"
	}
	return fmt.Sprintf("%d titles", total)
}
