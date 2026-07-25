// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strings"
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
			// "Trending now" — the items neighbouring the first featured one —
			// leads the library. Posters, like every browse row: the landscape
			// tile is reserved for continue-watching, where the 16:9 frame is
			// carrying a progress bar and a resume affordance rather than just
			// being a larger picture.
			upCards := make([]ui.El, 0, homeUpNextItems)
			for j := 1; j < len(items) && j <= homeUpNextItems; j++ {
				it := items[j]
				upCards = append(upCards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
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
	// advances the full-viewport hero slides; the library then rides UP over the
	// hero's floor, pulled into it by `overlap` so the first rail breaks the
	// bottom edge of the artwork rather than starting cleanly below it. Both live
	// in the Screen's edge-to-edge `bleed` slot (the rails own their gutter), so
	// the padded body collapses ($childCount 0). When enrichment failed for every
	// pick there is no hero and the rails stand alone.
	sheetEls := make([]ui.El, 0, len(rows)+2)
	sheetEls = append(sheetEls, ui.Prop("style", map[string]any{
		"direction": "column", "gap": 7,
		"px": "gutter", "pb": 9,
		"position": "relative", "z": "raised",
		"overlap": 7,
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
//
// The hero carries its own copy and controls — the kicker, the title treatment,
// the meta pills, the synopsis and a play/more-info pair. It reads as the
// landing surface rather than as a picture with a caption, and every affordance
// on it leads somewhere the viewer can already go: the detail screen it
// summarises.
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

	// The pills, in the order the eye reads them: how good, how old, how long,
	// what kind. Each is omitted rather than shown empty — a hero wearing "★ 0.0"
	// is worse than one wearing nothing.
	pills := make([]string, 0, 4)
	if m.Rating > 0 {
		pills = append(pills, fmt.Sprintf("★ %.1f", m.Rating))
	}
	if y := yearLabel(m.Year); y != "" {
		pills = append(pills, y)
	}
	if m.Runtime != "" {
		pills = append(pills, m.Runtime)
	}
	if len(m.Genres) > 0 {
		pills = append(pills, strings.Join(m.Genres, " · "))
	}

	detail := ui.Navigate(screenDetail, map[string]any{paramRef: refInput(it.Ref)})
	els := []ui.El{
		ui.Prop("variant", "feature"),
		ui.When(kicker != "", ui.Prop("kicker", kicker)),
		ui.Backdrop(s.art(m.Backdrop)),
		ui.When(m.Logo != "", ui.Logo(s.art(m.Logo))),
		ui.When(len(pills) > 0, ui.Meta(pills...)),
		ui.When(m.Overview != "", ui.Overview(m.Overview)),
		// The hero cannot offer Play itself: a catalog item is a virtual ref, and
		// there is no Part behind it until it has been materialised (ADR 0028).
		// An affordance with nothing behind it is the dead end ADR 0036 exists to
		// remove, so the primary says what it actually does — and the secondary
		// is the one act the hero *can* perform on a virtual item, dropped once
		// the item is already in the library.
		ui.Actions(
			ui.Button("More info", "primary", ui.IconName("info"), ui.OnTap(detail)),
			ui.When(!it.InLibrary, ui.Button("Add to library", "secondary", ui.IconName("plus"),
				ui.OnTap(ui.Invoke(importContentMutation, map[string]any{paramRef: refInput(it.Ref)})))),
		),
	}
	return ui.Hero(title, els...)
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

	els := make([]ui.El, 0, 6)
	els = append(els, ui.Prop("mediaType", string(work.Node.MediaType)))
	// The rail is landscape, so it wants the backdrop rather than the poster: a
	// 2:3 poster cropped to 16:9 is a band across the middle of the artwork. The
	// poster is the fallback, because a card with the wrong shape of art still
	// beats a card with none.
	art := poster
	if b := work.Node.Artwork.Backdrop; b != "" {
		art = s.art(b)
	}
	if art != "" {
		els = append(els, ui.Poster(art))
	}
	// Name the episode under the series title; a film's item has nothing to add.
	if item.Node.ItemType == v1.ItemEpisode && item.Node.Title != "" {
		els = append(els, ui.Subtitle(item.Node.Title))
	}
	if f := progressFraction(item.State); f > 0 {
		els = append(els, ui.Progress(f))
		if left := remainingLabel(item.State); left != "" {
			els = append(els, ui.ProgressLabel(left))
		}
	}
	els = append(els, ui.OnTap(ui.Invoke(playPartAction, map[string]any{
		paramPartID: string(item.State.PartID),
		paramNodeID: string(item.Node.ID),
		"title":     title,
		"poster":    poster,
	})))
	return ui.MediaTile(title, els...)
}

// remainingLabel is how much of an in-progress item is left, for the veil a
// continue-watching tile shows on approach ("42 min left"). Empty when the
// player never reported a length, or when the remainder rounds to nothing —
// "0 min left" on something a viewer is about to finish reads as a bug.
func remainingLabel(st v1.PlaybackState) string {
	left := st.Duration - st.Position
	if st.Duration <= 0 || left <= 0 {
		return ""
	}
	if h := int(left.Hours()); h > 0 {
		return fmt.Sprintf("%dh %02dm left", h, int(left.Minutes())%60)
	}
	if mins := int(left.Minutes()); mins > 0 {
		return fmt.Sprintf("%d min left", mins)
	}
	return ""
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
