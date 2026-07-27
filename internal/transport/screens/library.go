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

// libraryPageSize is how many works one page shows. It matches the application
// service's default, which is the number that actually applies; naming it here
// as well keeps the page label ("Page 2 of 7") arithmetically honest without the
// screen having to guess.
const libraryPageSize = 60

// libraryScreen renders one page of what the install owns.
func (s *Service) libraryScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	page := intParam(params, paramPage)
	if page < 0 {
		page = 0
	}

	res, err := s.content.ListLibrary(ctx, app.ListLibraryQuery{
		Caller: caller,
		Limit:  libraryPageSize,
		Offset: page * libraryPageSize,
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
		// A page past the end — a bookmark from when the library was larger, or
		// a hand-edited URL. The count is still true, so the screen says it and
		// offers the way back rather than looking like an empty library.
		return ui.Screen(
			ui.Title("Library"),
			ui.Subtitle(libraryCountLabel(res.Total)),
			ui.EmptyState(emptyIconNotFound,
				"There is no page "+fmt.Sprint(page+1),
				ui.Message("The library has "+libraryCountLabel(res.Total)+"."),
				ui.ActionSlot(ui.Button("Back to the first page", "primary",
					ui.OnTap(ui.Navigate(screenLibrary, nil)))),
			),
		).Build(), nil
	}

	cards := make([]ui.El, 0, len(res.Works))
	for _, work := range res.Works {
		cards = append(cards, s.libraryCard(work))
	}

	els := []ui.El{
		ui.Title("Library"),
		// The live count, and where in it this page sits. Both, because one
		// without the other is only half the question a person browsing a large
		// library is asking.
		ui.Subtitle(libraryCountLabel(res.Total) + " · " + libraryRangeLabel(res)),
		ui.Grid(ui.MinColumnWidth(196), ui.Group(cards...)),
	}
	if pager := libraryPager(page, res); pager != nil {
		els = append(els, pager)
	}
	return ui.Screen(els...).Build(), nil
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

// libraryRangeLabel says which slice of the library is on screen.
func libraryRangeLabel(res app.ListLibraryResult) string {
	first := res.Offset + 1
	last := res.Offset + len(res.Works)
	if first == last {
		return fmt.Sprintf("showing %d", first)
	}
	return fmt.Sprintf("showing %d–%d", first, last)
}

// libraryPager is the Previous/Next control under the grid, or nothing at all
// when the whole library fits on one page.
//
// It is the vocabulary's `Pagination`, which had existed and been emitted by
// nothing. The library is the first surface with a real total to page against —
// everything else pages a provider's catalog by asking for more, which is what
// `HasMore`/`LoadMore` express and is a different interaction from "page 3 of
// 7".
func libraryPager(page int, res app.ListLibraryResult) ui.El {
	hasPrev := page > 0
	hasNext := res.HasMore()
	if !hasPrev && !hasNext {
		return nil
	}
	pages := (res.Total + res.Limit - 1) / res.Limit
	return ui.Pagination(
		// FieldLabel, because `label` is the prop Pagination binds and that is
		// the generated helper for it. Spelling it ui.Prop("label", …) would
		// compile and render identically today and would not be checked against
		// the contract tomorrow.
		ui.FieldLabel(fmt.Sprintf("Page %d of %d", page+1, pages)),
		ui.HasPrev(hasPrev),
		ui.HasNext(hasNext),
		ui.PrevAction(ui.Navigate(screenLibrary, libraryPageParams(page-1))),
		ui.NextAction(ui.Navigate(screenLibrary, libraryPageParams(page+1))),
	)
}

// libraryPageParams is a page navigation's params, omitting the first page's
// index so the library's own route stays clean and a "back to the start" link
// and the nav item lead to the same place.
func libraryPageParams(page int) map[string]any {
	if page <= 0 {
		return nil
	}
	return map[string]any{paramPage: page}
}
