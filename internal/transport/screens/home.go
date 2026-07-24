// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"sync"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

const (
	homeMaxRows     = 6
	homeMaxRowItems = 20
	// homeContinueItems bounds the continue-watching rail — the most recently
	// watched, unfinished items. A rail, not an archive (ADR 0046).
	homeContinueItems = 12
	// homeUpNextItems bounds the "Up next" filmstrip docked on the hero floor —
	// the items neighbouring the featured one, drawn from the same first catalog.
	homeUpNextItems = 8
	// homeHeroSlides bounds the hero carousel: the top item of each of the first
	// few non-empty catalogs, auto-advancing behind the content sheet.
	homeHeroSlides = 5
)

// homeScreen is the default landing surface: a hero over rows of the enabled
// modules' catalogs (Cinemeta's Popular Movies/Series, etc. — ADR 0028's virtual
// plane, browsed not materialised). Each row is a carousel of cards that open a
// detail; the hero is the first catalog's first item, enriched with its backdrop
// and logo. Browsing is a read, so nothing here writes.
func (s *Service) homeScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	cats, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil {
		return nil, err
	}
	if len(cats.Catalogs) == 0 {
		return ui.Screen(ui.EmptyState(emptyIconCollections,
			"Nothing here yet — add an addon in Settings to browse content")).Build(), nil
	}

	// Render at most homeMaxRows rows. Each row's items are a remote round-trip,
	// so fetch them concurrently rather than serially — the landing page pays one
	// round-trip instead of a sum. We fetch only the catalogs we render (the first
	// homeMaxRows), bounding remote load to the visible rows; a catalog beyond that
	// is not fetched, and one that errors simply drops its row.
	catalogs := cats.Catalogs
	if len(catalogs) > homeMaxRows {
		catalogs = catalogs[:homeMaxRows]
	}
	itemsByCatalog := make([]app.ListCatalogItemsResult, len(catalogs))
	var wg sync.WaitGroup
	for i, c := range catalogs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A downed catalog leaves its slot empty, which the assembly below skips
			// — the same effect as the serial code's continue-on-error.
			items, err := s.content.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
				Caller: caller, ModuleID: c.ModuleID, CatalogID: c.Catalog.ID, NativeType: c.Catalog.NativeType,
			})
			if err == nil {
				itemsByCatalog[i] = items
			}
		}()
	}
	wg.Wait()

	// Assemble the page as a widget tree. The featured banner comes from the
	// first non-empty catalog's first item (one further round-trip to enrich it),
	// spanning the Screen's full-bleed slot with an "Up next" filmstrip of its
	// neighbours docked on its floor; then a carousel row per non-empty catalog.
	rows := make([]ui.El, 0, len(catalogs)+1)
	type heroPick struct {
		item   v1.CatalogItem
		kicker string
	}
	var picks []heroPick
	var upNext ui.El
	for i, c := range catalogs {
		items := itemsByCatalog[i].Items
		if len(items) == 0 {
			continue
		}
		// The hero carousel takes the top item of each of the first few catalogs.
		if len(picks) < homeHeroSlides {
			picks = append(picks, heroPick{item: items[0], kicker: c.Catalog.Name})
		}
		if upNext == nil {
			// "Trending now" — the items neighbouring the first featured one — leads
			// the library as a rail of glass MediaTiles, the showcase row for the
			// acrylic material (the edge light tracks the hero art across the row).
			// Library rows below stay plain PosterCards.
			upCards := make([]ui.El, 0, homeUpNextItems)
			for j := 1; j < len(items) && j <= homeUpNextItems; j++ {
				it := items[j]
				upCards = append(upCards, s.mediaTile(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
			}
			if len(upCards) > 0 {
				upNext = ui.Section("Trending now", ui.Carousel(upCards...))
			}
		}
		cards := make([]ui.El, 0, homeMaxRowItems)
		for j, it := range items {
			if j >= homeMaxRowItems {
				break
			}
			cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
		}
		rows = append(rows, ui.Section(c.Catalog.Name, ui.Carousel(cards...)))
	}
	if len(rows) == 0 {
		return ui.Screen(ui.EmptyState(emptyIconCollections,
			"Nothing to show yet — try adding an addon in Settings")).Build(), nil
	}

	// Enrich the featured picks into hero banners concurrently — each is a further
	// metadata round-trip (backdrop/logo). Order is preserved; a pick whose
	// enrichment fails drops out.
	slides := make([]ui.El, len(picks))
	var hg sync.WaitGroup
	for i, p := range picks {
		hg.Add(1)
		go func() {
			defer hg.Done()
			if h := s.heroFromItem(ctx, caller, p.item, p.kicker); h != nil {
				slides[i] = h
			}
		}()
	}
	hg.Wait()
	heroSlides := make([]ui.El, 0, len(slides))
	for _, h := range slides {
		if h != nil {
			heroSlides = append(heroSlides, h)
		}
	}

	// The home is a cinematic backdrop the content rides over. A Rotator auto-
	// advances the hero slides (mostly artwork — no pills/overview/buttons) and is
	// `sticky`, so it stays put while the library, carried on a glass "sheet",
	// scrolls UP over it: the sheet's acrylic top edge catches the active hero's
	// light on the way past. Both live in the Screen's edge-to-edge `bleed` slot
	// (the sheet owns its own gutter), so the padded body collapses ($childCount 0).
	// When enrichment failed for every pick there's no hero; the sheet stands alone.
	sheetEls := make([]ui.El, 0, len(rows)+2)
	sheetEls = append(sheetEls, ui.Prop("style", map[string]any{
		"glass": true, "bg": "bg", "radius": "xl",
		"direction": "column", "gap": 8,
		"px": "gutter", "pt": 8, "pb": 9,
		"position": "relative", "z": "raised",
	}))
	// Continue watching leads the sheet: the most personal rail, above the
	// browse rows below it. It is gated by having something in progress — an
	// install with no playback consumer has nothing here and shows nothing
	// (ADR 0036). (When the metadata addons are down the catalogs are empty and
	// this whole screen short-circuits above; surfacing the rail there is
	// cache-first rendering, ADR 0052, slice 4.)
	if cw := s.continueWatchingSection(ctx, caller); cw != nil {
		sheetEls = append(sheetEls, cw)
	}
	if upNext != nil {
		sheetEls = append(sheetEls, upNext)
	}
	sheetEls = append(sheetEls, rows...)
	sheet := ui.Component("Box", sheetEls...)

	bleed := make([]ui.El, 0, 2)
	if len(heroSlides) > 0 {
		rotEls := make([]ui.El, 0, len(heroSlides)+1)
		rotEls = append(rotEls, ui.Prop("interval", 6000))
		rotEls = append(rotEls, heroSlides...)
		bleed = append(bleed, ui.Component("Rotator", rotEls...))
	}
	bleed = append(bleed, sheet)
	return ui.Screen(ui.Slot("bleed", bleed...)).Build(), nil
}

// heroFromItem builds the home's featured banner from a catalog item, enriching
// it with the backdrop, logo and overview its lightweight card lacks (ADR 0034).
// It is full-bleed and tagged with the catalog it leads (the `kicker`). A
// metadata fetch that fails just yields no hero (nil) rather than failing the
// home screen.
func (s *Service) heroFromItem(ctx context.Context, caller v1.Caller, it v1.CatalogItem, kicker string) *ui.Element {
	prev, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: it.Ref})
	if err != nil {
		return nil
	}
	m := prev.Metadata
	title := m.Title
	if title == "" {
		title = it.Title
	}

	// The home hero is artwork first: just the catalog kicker + the title (or logo)
	// over the backdrop — no overview, no meta pills, no buttons. That "detail page"
	// chrome belongs on the detail screen; here the poster is the hero.
	return ui.Hero(title,
		ui.Prop("variant", "feature"),
		ui.When(kicker != "", ui.Prop("kicker", kicker)),
		ui.Backdrop(s.art(m.Backdrop)),
		ui.When(m.Logo != "", ui.Logo(s.art(m.Logo))),
	)
}

// continueWatchingSection renders the home's continue-watching rail from the
// in-progress list (ADR 0046): the items a viewer has started and not finished,
// most recently touched first. It returns nil when there is nothing in progress
// — the rail is a capability-gated affordance (ADR 0036), and an install with no
// playback consumer simply has no rail — and when the query fails, so a rail
// that cannot load never takes the home screen down with it.
func (s *Service) continueWatchingSection(ctx context.Context, caller v1.Caller) ui.El {
	res, err := s.content.ListInProgress(ctx, v1.ListInProgressQuery{Caller: caller, Limit: homeContinueItems})
	if err != nil {
		// Dropped, but not silently: "nothing in progress" and "the query failed"
		// must stay distinguishable, which is a difference only a log can carry.
		telemetry.From(ctx).For("screens").Warn("continue-watching query failed; omitting the rail",
			telemetry.Err(err))
		return nil
	}
	if len(res.Items) == 0 {
		return nil
	}

	// Each card needs its Work's poster and title, one indexed read apiece — a
	// database read, not a metadata round-trip, because the art is stored
	// (ADR 0071). Fetch them concurrently, as the hero enrichment does, so the
	// rail costs one round-trip rather than a sum; a card whose read fails drops
	// out rather than failing the rail.
	cards := make([]ui.El, len(res.Items))
	var wg sync.WaitGroup
	for i, item := range res.Items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cards[i] = s.continueCard(ctx, caller, item)
		}()
	}
	wg.Wait()

	out := make([]ui.El, 0, len(cards))
	for _, c := range cards {
		if c != nil {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return ui.Section("Continue watching", ui.Carousel(out...))
}

// continueCard renders one continue-watching item: the work's poster with a
// resume-progress bar, the work title (and, for a series, the episode beneath
// it), and a tap that resumes the same release at the stored position.
//
// The tap invokes playPart directly rather than opening a detail: a rail item is
// a node, and a node cannot be turned back into the provider-bearing ref a rich
// detail needs (ADR 0071), so one-tap resume is both the better affordance and
// the only one reachable. The card carries the Part last played and the node the
// position is keyed to; the Platform reads the offset itself (ADR 0046), so a
// stale offset costs the offset, never the play.
func (s *Service) continueCard(ctx context.Context, caller v1.Caller, item v1.InProgressItem) ui.El {
	// Without the release that produced the position there is nothing to resume;
	// such a row should not have come back from the in-progress query, but a card
	// that cannot act is worse than one absent.
	if item.State.PartID == "" {
		return nil
	}
	// The poster and title live on the Work, not on the episode or feature item
	// the position is keyed to (ADR 0013 attaches Parts to items; ADR 0071 stores
	// art on the work).
	work, err := s.content.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: item.Node.WorkID})
	if err != nil {
		return nil
	}
	title := work.Node.Title

	poster := ""
	if p := work.Node.Artwork.Poster; p != "" {
		poster = s.art(p)
	}

	els := make([]ui.El, 0, 4)
	if poster != "" {
		els = append(els, ui.Poster(poster))
	}
	// Name the episode under the series title; a film's item has nothing to add.
	if item.Node.ItemType == v1.ItemEpisode && item.Node.Title != "" {
		els = append(els, ui.Subtitle(item.Node.Title))
	}
	if f := progressFraction(item.State); f > 0 {
		els = append(els, ui.Progress(f))
	}
	els = append(els, ui.OnTap(ui.Invoke(playPartAction, map[string]any{
		paramPartID: string(item.State.PartID),
		paramNodeID: string(item.Node.ID),
		"title":     title,
		"poster":    poster,
	})))
	return ui.PosterCard(title, string(work.Node.MediaType), els...)
}

// progressFraction is a viewer's position as a 0..1 fraction for a resume bar,
// 0 when the player never reported a length (the bar is then omitted rather than
// drawn full or empty).
func progressFraction(st v1.PlaybackState) float64 {
	if st.Duration <= 0 {
		return 0
	}
	f := st.Position.Seconds() / st.Duration.Seconds()
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
