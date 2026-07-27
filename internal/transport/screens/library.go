// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
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
	genre := stringParam(params, paramGenre)

	// One read of everything loaded so far, rather than a read per page: the
	// library is the install's own rows and can be windowed directly, where a
	// provider's catalog has to be walked a page at a time.
	window := (page + 1) * libraryPageSize
	if window > libraryWindowCap {
		window = libraryWindowCap
	}
	res, err := s.content.ListLibrary(ctx, app.ListLibraryQuery{
		Caller: caller,
		Genres: genreFilter(genre),
		Limit:  window,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}

	if res.Total == 0 && genre == "" {
		// The genuinely empty library. It is worth a different sentence from
		// "this page is past the end" below, because one is a new install and
		// the other is a stale link, and telling somebody with an empty library
		// to go back a page would be nonsense.
		//
		// A *narrowed* browse that matches nothing is a third thing again and
		// must not land here: telling somebody whose library holds eight hundred
		// titles that it is empty because they pressed a chip would be the screen
		// lying about the library. The facets are built from what is on the shelf
		// so this should be unreachable by pressing — but a stale link carries a
		// genre that may since have left, and that is exactly when a screen has
		// to be right.
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

	if len(res.Works) == 0 && genre == "" {
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

	if len(res.Works) == 0 {
		// Narrowed to nothing — a stale link naming a genre no work carries any
		// more. It has to come *after* the past-the-end case and be guarded
		// against it, because both are "no rows on this page" and only this one
		// can offer the way out that actually helps: the facet row itself, which
		// is still drawn.
		return ui.Screen(
			ui.Title("Library"),
			ui.Subtitle("Nothing matches "+genre),
			libraryFacets(res.Facets, genre),
			ui.EmptyState(emptyIconNotFound,
				"No "+genre+" titles",
				ui.Message("Nothing in the library carries that genre. It may have been the only title, and it may have left."),
				ui.ActionSlot(ui.Button("Show everything", "primary",
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
		// The narrowing travels with the page. A `query` action replaces the
		// content region and re-renders the whole window, so a next-page action
		// that dropped the genre would quietly widen the browse mid-scroll —
		// the same class of mistake as a provider that ignores a filter it does
		// not recognise, and just as invisible.
		next := map[string]any{paramPage: page + 1}
		if genre != "" {
			next[paramGenre] = genre
		}
		grid = append(grid, ui.HasMore(true), ui.LoadMore(ui.Query(screenLibrary, next)))
	}

	return ui.Screen(
		ui.Title("Library"),
		ui.Subtitle(librarySubtitle(res, more, window, genre)),
		libraryFacets(res.Facets, genre),
		ui.Grid(grid...),
	).Build(), nil
}

// genreFilter renders the screen's one selected genre as the store query's
// conjunctive list. Empty selects nothing, which is every work.
func genreFilter(genre string) []string {
	if genre == "" {
		return nil
	}
	return []string{genre}
}

// maxLibraryFacets is how many genre chips one row offers.
//
// A library fed by several sources accumulates their vocabularies unreconciled —
// "Sci-Fi" from one and "Science Fiction" from another are two genres because
// two sources say so — so the tail of this list is long and thin. The chips are
// ordered by how many works carry them, so the cut takes the ones nobody would
// press.
const maxLibraryFacets = 14

// libraryFacets is the row of narrowings, built from what is actually on the
// shelf.
//
// **Every chip returns something**, because the values come from a read over the
// library rather than from a vocabulary written down anywhere — which is the only
// way to be right about a genre list assembled from several sources' words. The
// selected chip toggles off rather than needing a separate clear, so the control
// is reversible by the same press that set it.
func libraryFacets(facets contracts.Facets, selected string) ui.El {
	if len(facets.Genres) == 0 {
		// Nothing carries a genre — a library imported before genres were stored,
		// or from a source that names none. An empty row is drawn as nothing at
		// all rather than as a heading over a gap.
		return nil
	}

	chips := make([]ui.El, 0, maxLibraryFacets+1)
	if selected != "" {
		// "All" leads, and only when something is selected: a row whose first
		// chip is always lit reads as a filter that is always on.
		chips = append(chips, ui.FilterChip("All", false,
			ui.OnTap(ui.Query(screenLibrary, nil))))
	}
	for i, g := range facets.Genres {
		if i >= maxLibraryFacets && g.Value != selected {
			// The selection is kept whatever its rank, so a chip a user pressed
			// cannot fall off the row it was pressed on.
			continue
		}
		params := map[string]any{paramGenre: g.Value}
		if g.Value == selected {
			// Pressing the lit chip clears it.
			params = nil
		}
		chips = append(chips, ui.FilterChip(g.Value, g.Value == selected,
			ui.FacetCount(strconv.Itoa(g.Count)),
			ui.OnTap(ui.Query(screenLibrary, params))))
	}
	return ui.Stack("horizontal", 2, ui.Wrap(true), ui.Group(chips...))
}

// librarySubtitle is the live count, and — only when the scroll has stopped
// short of the whole library — what is actually on screen.
//
// While more is still loading it says the total alone: a number that ticks
// upward as somebody scrolls is noise, and the total is the fact they came for.
func librarySubtitle(res app.ListLibraryResult, more bool, window int, genre string) string {
	count := libraryCountLabel(res.Total)
	if genre != "" {
		// The count is of the *narrowed* library, so it says what it counted.
		// "81 titles" under a lit Drama chip would read as the whole library and
		// be a smaller number than the one before it — which is the shape of a
		// screen that has silently lost something.
		count += " in " + genre
	}
	if more && window >= libraryWindowCap {
		return fmt.Sprintf("%s · showing the first %d — search to narrow it down",
			count, len(res.Works))
	}
	return count
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
