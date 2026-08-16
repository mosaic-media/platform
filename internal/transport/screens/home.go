// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// watched, unfinished items. A rail, not an archive (platform#26).
	homeContinueItems = 12
	// homeUpNextItems bounds the "Up next" filmstrip docked on the hero floor —
	// the items neighbouring the featured one, drawn from the same first catalog.
	homeUpNextItems = 8
	// homeHeroSlides bounds the hero carousel: the top item of each of the first
	// few non-empty catalogs, auto-advancing behind the content sheet.
	homeHeroSlides = 5
)

// homeScreen is the default landing surface: a full-viewport cinematic hero
// over rows of the enabled modules' catalogs (platform#18's virtual plane, browsed
// not materialised — TMDB's, on a keyed deployment, since contracts#18 made the
// browse roles ranked). Each row is a carousel of cards that open a detail and a
// heading that opens the whole catalog; the hero rotates through the top item of
// each of the first few catalogs, enriched with its backdrop and logo. Browsing
// is a read, so nothing here writes.
func (s *Service) homeScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	report := reportFrom(ctx)
	refresh := refreshing(ctx)

	cats, err := s.content.BrowseCatalogs(ctx, app.BrowseCatalogsQuery{Caller: caller, Refresh: refresh})
	if err != nil {
		return nil, err
	}
	report.note(cats.Answer.From == app.AnswerSnapshot, cats.Answer.Stale, cats.Answer.TakenAt, cats.Answer.Failed)
	if len(cats.Catalogs) == 0 {
		return ui.Screen(s.sourcesEmptyState(cats.Answer)).Build(), nil
	}

	// One read, in the same pass that builds the screen (platform#59): asking per
	// row would be a round trip per row on the surface every session lands on.
	// It is applied before the items are fetched, which is the point of doing it
	// here rather than while assembling — a row this viewer hid must not cost a
	// provider round trip to draw nothing with.
	composition := s.content.HomeCompositionFor(ctx, caller)
	catalogs := arrangeCatalogs(cats.Catalogs, composition)

	if len(catalogs) > homeMaxRows {
		catalogs = catalogs[:homeMaxRows]
	}
	itemsByCatalog := s.fetchRowItems(ctx, caller, catalogs, refresh, report)

	rows, picks, upNext := s.catalogRows(catalogs, itemsByCatalog)
	if len(rows) == 0 {
		// Every catalog answered emptily or not at all, and which of those it was
		// is a distinction only the answers can make (platform#30).
		return ui.Screen(s.sourcesEmptyState(mergedItemAnswer(itemsByCatalog))).Build(), nil
	}

	metas := s.enrichPicks(ctx, caller, picks)
	heroSlides := s.heroSlidesFor(picks, metas)
	heroed := len(heroSlides) > 0
	sheet := s.homeSheet(ctx, caller, report, composition, rows, upNext, heroed)

	// A Rotator auto-advances the full-viewport hero slides and the sheet rides up
	// over their floor, both in the Screen's edge-to-edge bleed slot (the rails own
	// their gutter), so the padded body collapses ($childCount 0).
	bleed := make([]ui.El, 0, 2)
	if heroed {
		rotEls := make([]ui.El, 0, len(heroSlides)+1)
		rotEls = append(rotEls, ui.Prop("interval", 6000))
		rotEls = append(rotEls, heroSlides...)
		bleed = append(bleed, ui.Component("Rotator", rotEls...))
	}
	bleed = append(bleed, sheet)
	return ui.Screen(ui.Slot("bleed", bleed...)).Build(), nil
}

// fetchRowItems fetches the items of every catalog that will be rendered,
// concurrently.
//
// Each row's items are a remote round-trip when they are not already stored, so
// the landing page pays one round-trip instead of a sum. Only the catalogs that
// are rendered are fetched, bounding remote load to the visible rows. A catalog
// that neither answers nor has a stored answer leaves its slot empty and drops
// its row, counted in the report rather than discarded, so a screen that lost
// every row can say why.
func (s *Service) fetchRowItems(ctx context.Context, caller v1.Caller, catalogs []app.ModuleCatalog, refresh bool, report *Report) []app.BrowseCatalogItemsResult {
	itemsByCatalog := make([]app.BrowseCatalogItemsResult, len(catalogs))
	var wg sync.WaitGroup
	for i, c := range catalogs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := s.content.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
				Caller: caller, ModuleID: c.ModuleID, CatalogID: c.Catalog.ID, NativeType: c.Catalog.NativeType,
				Refresh: refresh,
			})
			if err != nil {
				// A read that failed at the boundary rather than at the source —
				// the provider was deregistered between listing the catalogs and
				// asking one for its items. Its row drops.
				telemetry.From(ctx).For("screens").Warn("home could not read a catalog",
					telemetry.String("module", c.ModuleID),
					telemetry.String("catalog", c.Catalog.ID), telemetry.Err(err))
				return
			}
			itemsByCatalog[i] = items
			report.note(items.Answer.From == app.AnswerSnapshot, items.Answer.Stale,
				items.Answer.TakenAt, items.Answer.Failed)
		}()
	}
	wg.Wait()
	return itemsByCatalog
}

// catalogRows builds a carousel row per non-empty catalog, and with them the
// hero picks — the top item of each of the first few — and the "Trending now"
// filmstrip the first of them contributes.
func (s *Service) catalogRows(catalogs []app.ModuleCatalog, itemsByCatalog []app.BrowseCatalogItemsResult) ([]ui.El, []heroPick, ui.El) {
	rows := make([]ui.El, 0, len(catalogs)+1)
	var picks []heroPick
	var upNext ui.El
	for i, c := range catalogs {
		items := itemsByCatalog[i].Items
		if len(items) == 0 {
			continue
		}
		if len(picks) < homeHeroSlides {
			picks = append(picks, heroPick{item: items[0], kicker: c.Catalog.Name})
		}
		if upNext == nil {
			upNext = s.trendingSection(c, items)
		}
		cards := make([]ui.El, 0, homeMaxRowItems)
		for j, it := range items {
			if j >= homeMaxRowItems {
				break
			}
			cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
		}
		// Each row leads to its whole catalog. A rail is a window onto a
		// collection, and without this the twentieth item is where the collection
		// ends as far as anyone browsing can tell (platform#18).
		rows = append(rows, ui.Section(c.Catalog.Name,
			ui.Gap(4),
			ui.ActionLabel("See all"),
			ui.OnTap(ui.Navigate(screenCatalog, map[string]any{
				paramModuleID: c.ModuleID, paramCatalogID: c.Catalog.ID, paramNativeType: c.Catalog.NativeType,
				paramTitle: c.Catalog.Name,
			})),
			ui.Carousel(cards...)))
	}
	return rows, picks, upNext
}

// trendingSection is the rail that leads the library: the items neighbouring the
// first featured one, under the catalog they came from. Nil when there are none.
//
// Posters, like every browse row: the landscape tile is reserved for
// continue-watching, where the 16:9 frame is carrying a progress bar and a
// resume affordance rather than just being a larger picture.
func (s *Service) trendingSection(c app.ModuleCatalog, items []v1.CatalogItem) ui.El {
	upCards := make([]ui.El, 0, homeUpNextItems)
	for j := 1; j < len(items) && j <= homeUpNextItems; j++ {
		it := items[j]
		upCards = append(upCards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
	}
	if len(upCards) == 0 {
		return nil
	}
	return ui.Section("Trending now",
		ui.Gap(4),
		ui.ActionLabel("See all"),
		ui.OnTap(ui.Navigate(screenCatalog, map[string]any{
			paramModuleID: c.ModuleID, paramCatalogID: c.Catalog.ID, paramNativeType: c.Catalog.NativeType,
			paramTitle: c.Catalog.Name,
		})),
		ui.Carousel(upCards...))
}

// enrichPicks fetches each featured pick's backdrop, logo and synopsis — what a
// catalog card lacks — concurrently, and nil for a pick that could not be read.
//
// In one pass before any slide is built, because a slide needs its neighbour's
// artwork as well as its own: the up-next dock is a landscape tile and a catalog
// item carries only a poster, so that backdrop is one already being fetched for
// the neighbour's own slide rather than a second round-trip for the same title.
func (s *Service) enrichPicks(ctx context.Context, caller v1.Caller, picks []heroPick) []*v1.ContentMetadata {
	metas := make([]*v1.ContentMetadata, len(picks))
	var hg sync.WaitGroup
	for i, p := range picks {
		hg.Add(1)
		go func() {
			defer hg.Done()
			prev, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: p.item.Ref})
			if err != nil {
				return
			}
			metas[i] = &prev.Metadata
		}()
	}
	hg.Wait()
	return metas
}

// heroSlidesFor builds the hero carousel from the picks that enriched.
//
// Each slide docks the one after it — and the last docks the first, because the
// rotation does — so the carousel says what is coming rather than only what is
// here, and a viewer who wants it now can open it rather than waiting out the
// dwell. A neighbour whose enrichment failed simply gets no dock.
func (s *Service) heroSlidesFor(picks []heroPick, metas []*v1.ContentMetadata) []ui.El {
	heroSlides := make([]ui.El, 0, len(picks))
	for i, p := range picks {
		if metas[i] == nil {
			continue
		}
		var next *heroPick
		var nextMeta *v1.ContentMetadata
		if len(picks) > 1 {
			j := (i + 1) % len(picks)
			next, nextMeta = &picks[j], metas[j]
		}
		heroSlides = append(heroSlides, s.heroSlide(p, *metas[i], next, nextMeta))
	}
	return heroSlides
}

// homeSheet is the library that rides up over the hero's floor: the staleness
// banner, continue watching, "Trending now", then a row per catalog.
//
// The overlap that pulls it into the artwork applies only when there is a hero to
// overlap. It is a negative offset into the artwork above, so with no hero it
// would pull the first rail up under the brand bar and crop its cards — and the
// no-hero branch is the ordinary degraded screen under cache-first rendering,
// not a rare one.
func (s *Service) homeSheet(ctx context.Context, caller v1.Caller, report *Report,
	composition app.HomeComposition, rows []ui.El, upNext ui.El, heroed bool) ui.El {
	sheetStyle := map[string]any{
		"direction": "column", "gap": 7,
		"px": "gutter", "pb": 9,
		"position": "relative", "z": "raised",
	}
	if heroed {
		sheetStyle["overlap"] = 7
	} else {
		sheetStyle["pt"] = 9
	}
	sheetEls := make([]ui.El, 0, len(rows)+3)
	sheetEls = append(sheetEls, ui.Prop("style", sheetStyle))
	if b := s.stalenessBanner(report); b != nil {
		sheetEls = append(sheetEls, b)
	}
	// Continue watching leads the sheet: the most personal rail, above the browse
	// rows below it. Capability omission composes ahead of preference, which is
	// the order platform#59 requires — a viewer cannot un-hide something they were
	// never able to use, and a hidden row is not evidence of a permission.
	if !composition.Hides(homeRowContinue) {
		if cw := s.continueWatchingSection(ctx, caller); cw != nil {
			sheetEls = append(sheetEls, cw)
		}
	}
	if upNext != nil {
		sheetEls = append(sheetEls, upNext)
	}
	sheetEls = append(sheetEls, rows...)
	return ui.Component("Box", sheetEls...)
}

// homeRowContinue is the continue-watching rail's key in a viewer's
// composition. It is not a catalog, so it is named rather than derived.
const homeRowContinue = "continue"

// homeRowKey identifies one catalog row across renders, restarts and reorderings.
//
// The module, the native type and the catalog id together, because a catalog id
// is only unique within a type within a module — and because a stable key is
// what makes this a decision rather than a position: a viewer who hid "Trending
// Films" has hidden that catalog, not the third row.
func homeRowKey(c app.ModuleCatalog) string {
	return "catalog:" + c.ModuleID + ":" + c.Catalog.NativeType + ":" + c.Catalog.ID
}

// arrangeCatalogs puts the catalogs into this viewer's order and drops the ones
// they hid.
//
// The hero and the "Trending now" rail follow from the result rather than being
// arranged separately: both are drawn from the first catalog that has items, so
// a viewer who moves a row to the top gets its top title on the hero. One
// decision, not three.
func arrangeCatalogs(catalogs []app.ModuleCatalog, composition app.HomeComposition) []app.ModuleCatalog {
	byKey := make(map[string]app.ModuleCatalog, len(catalogs))
	keys := make([]string, 0, len(catalogs))
	for _, c := range catalogs {
		key := homeRowKey(c)
		if _, seen := byKey[key]; seen {
			continue
		}
		byKey[key] = c
		keys = append(keys, key)
	}
	out := make([]app.ModuleCatalog, 0, len(catalogs))
	for _, key := range composition.Arrange(keys) {
		if composition.Hides(key) {
			continue
		}
		out = append(out, byKey[key])
	}
	return out
}

// sourcesEmptyState is what home draws when it has no rows: two different
// states that must never render the same (platform#30).
//
// "Nothing configured" is advice — an install with no source installed is being
// told the one thing that will fix it. "The sources are unreachable" is a report
// — an install with a source that is not answering is being told what is wrong,
// and pointing it at Settings would send somebody to reconfigure a working
// addon. The distinction is drawn from the answers rather than from the count,
// because a count cannot tell the two apart.
func (s *Service) sourcesEmptyState(answer app.BrowseAnswer) ui.El {
	if len(answer.Failed) == 0 {
		return ui.EmptyState(emptyIconCollections,
			"Nothing here yet — add an addon in Settings to browse content")
	}
	return ui.EmptyState(emptyIconNotFound,
		"Your sources are not answering right now. Nothing is wrong with your setup — "+
			"this screen will fill in as soon as they come back.")
}

// mergedItemAnswer folds the per-catalog answers back into one, for the empty
// state that has to decide between advice and a report.
func mergedItemAnswer(results []app.BrowseCatalogItemsResult) app.BrowseAnswer {
	out := app.BrowseAnswer{From: app.AnswerLive}
	for _, r := range results {
		for _, f := range r.Answer.Failed {
			if !contains(out.Failed, f) {
				out.Failed = append(out.Failed, f)
			}
		}
	}
	return out
}

// stalenessBanner is the screen saying out loud that it is showing stored
// answers because its sources are not answering, and how old they are.
//
// It is nil unless a source actually failed. A snapshot served while a
// revalidation is in flight is about to be replaced by the live result, and a
// banner on every one of those would be a permanent fixture that nobody reads —
// which is how a warning stops being a warning.
func (s *Service) stalenessBanner(report *Report) ui.El {
	if !report.FromSnapshot() || len(report.Failed()) == 0 {
		return nil
	}
	return ui.Banner("Showing what was saved "+ageLabel(s.now().Sub(report.TakenAt()))+
		". Your sources are not answering, so this may be out of date.", "warning")
}

// ageLabel is how long ago something was, in the words a sentence about a stale
// screen wants: "just now", "12 minutes ago", "2 days ago".
//
// Rounded down to the unit, because a screen claiming to be an hour old when it
// is 61 minutes old is a smaller lie than one claiming two hours.
func ageLabel(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return counted(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return counted(int(d.Hours()), "hour") + " ago"
	default:
		return counted(int(d.Hours()/24), "day") + " ago"
	}
}

// counted renders a number with its unit, pluralised. Distinct from plural
// beside it, which pluralises a word and does not carry the count.
func counted(n int, unit string) string {
	return strconv.Itoa(n) + " " + plural(n, unit)
}

// heroPick is a catalog item chosen to lead the home, with the catalog name it
// leads under. Named at package scope because a slide is built from its own
// pick and its neighbour's.
type heroPick struct {
	item   v1.CatalogItem
	kicker string
}

// heroSlide builds one featured banner from an already-enriched pick — the
// backdrop, logo and synopsis a catalog card lacks (sdk#3) — tagged with the
// catalog it leads, and docking the slide that follows it.
//
// The hero carries its own copy and controls: the kicker, the title treatment,
// the meta pills, the synopsis and a play/more-info pair. It reads as the
// landing surface rather than as a picture with a caption, and every affordance
// on it leads somewhere the viewer can already go.
func (s *Service) heroSlide(p heroPick, m v1.ContentMetadata, next *heroPick, nextMeta *v1.ContentMetadata) ui.El {
	title := m.Title
	if title == "" {
		title = p.item.Title
	}

	// The pills, in the order the eye reads them: how good, how old, how long,
	// what kind. Each is omitted rather than shown empty — a hero wearing
	// "★ 0.0" is worse than one wearing nothing.
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

	detail := ui.Navigate(screenDetail, map[string]any{paramRef: refInput(p.item.Ref)})
	els := []ui.El{
		ui.Prop("variant", "feature"),
		ui.When(p.kicker != "", ui.Prop("kicker", p.kicker)),
		ui.Backdrop(s.art(m.Backdrop)),
		ui.When(m.Logo != "", ui.Logo(s.art(m.Logo))),
		ui.When(len(pills) > 0, ui.Meta(pills...)),
		ui.When(m.Overview != "", ui.Overview(m.Overview)),
		// The hero cannot offer Play itself: a catalog item is a virtual ref, and
		// there is no Part behind it until it has been materialised (platform#18).
		// An affordance with nothing behind it is the dead end platform#24 exists to
		// remove, so the primary says what it actually does — and the secondary
		// is the one act the hero can perform on a virtual item, dropped once the
		// item is already in the library.
		ui.Actions(
			ui.Button("More info", "primary", ui.IconName("info"), ui.OnTap(detail)),
			ui.When(!p.item.InLibrary, ui.Button("Add to library", "secondary", ui.IconName("plus"),
				ui.OnTap(ui.Invoke(importContentMutation, map[string]any{paramRef: refInput(p.item.Ref)})))),
		),
	}
	if next != nil && nextMeta != nil {
		els = append(els, ui.Rail(
			ui.Text(ui.Prop("text", "Up next"), ui.Prop("style", map[string]any{
				"variant": "xs", "color": "text-faint",
				"transform": "uppercase", "tracking": "wide",
			})),
			s.upNextTile(*next, *nextMeta),
		))
	}
	return ui.Hero(title, els...)
}

// upNextTile is the card docked on the hero floor: the slide that follows this
// one, as a landscape tile of its backdrop with its title laid over the
// artwork.
//
// It is a MediaTile rather than a PosterCard because the dock is a wide slot on
// a wide surface — a 2:3 poster standing in it reads as a different screen's
// component wedged into the hero, which is what it was. The backdrop is the
// enrichment already fetched for that slide's own turn in the rotation, so the
// dock costs no round-trip of its own; the poster stands in when a title has no
// backdrop, since a tile of the wrong shape still beats an empty frame.
//
// The width is set here rather than in the definition. A hero's rail is a dock,
// not a column, and a card with no width fills whatever it is put in.
func (s *Service) upNextTile(p heroPick, m v1.ContentMetadata) ui.El {
	title := m.Title
	if title == "" {
		title = p.item.Title
	}
	art := m.Backdrop
	if art == "" {
		art = p.item.Poster
	}
	tile := ui.MediaTile(title,
		ui.Prop("mediaType", string(p.item.Ref.MediaType)),
		ui.OverlayTitle(true),
		ui.When(art != "", ui.Poster(s.art(art))),
		ui.When(yearLabel(m.Year) != "", ui.Subtitle(yearLabel(m.Year))),
		ui.OnTap(ui.Navigate(screenDetail, map[string]any{paramRef: refInput(p.item.Ref)})),
	)
	return ui.Component("Box", ui.Prop("style", map[string]any{"width": 236}), tile)
}

// continueWatchingSection renders the home's continue-watching rail from the
// in-progress list (platform#26): the items a viewer has started and not finished,
// most recently touched first. It returns nil when there is nothing in progress
// — the rail is a capability-gated affordance (platform#24), and an install with no
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

	cards := s.continueCards(ctx, caller, res.Items)
	if len(cards) == 0 {
		return nil
	}
	// The count is the cards actually rendered rather than the query's total: one
	// whose Work read failed dropped out, and a heading claiming twelve over a rail
	// of eleven is a small lie that is very easy to ship.
	//
	// The track is the tile's own width — 16:9 stills at 328, not 2:3 posters at
	// 196, and at the browse default the art collapses to a third of its height,
	// the same defect the detail screen's cast and related rails had. The home's
	// rails also sit tighter under their headings than a settings panel's sections
	// do: 14 rather than 24. Both are the design's, measured.
	return ui.Section("Continue watching",
		ui.Subtitle(strconv.Itoa(len(cards))),
		ui.Gap(4),
		ui.Carousel(ui.ItemWidth(328), ui.Group(cards...)))
}

// continueCards renders one card per in-progress item, concurrently, dropping
// the ones whose Work could not be read. Each card is its Work's poster and
// title, one indexed read apiece — a database read, not a metadata round-trip,
// because the art is stored (platform#45) — so the rail costs one round-trip
// rather than a sum.
func (s *Service) continueCards(ctx context.Context, caller v1.Caller, items []v1.InProgressItem) []ui.El {
	cards := make([]ui.El, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
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
	return out
}

// continueCard renders one continue-watching item: the work's poster with a
// resume-progress bar, the work title (and, for a series, the episode beneath
// it), and a tap that resumes the same release at the stored position.
//
// The tap invokes playPart directly rather than opening a detail: a rail item is
// a node, and a node cannot be turned back into the provider-bearing ref a rich
// detail needs (platform#45), so one-tap resume is both the better affordance and
// the only one reachable. The card carries the Part last played and the node the
// position is keyed to; the Platform reads the offset itself (platform#26), so a
// stale offset costs the offset, never the play.
func (s *Service) continueCard(ctx context.Context, caller v1.Caller, item v1.InProgressItem) ui.El {
	// Without the release that produced the position there is nothing to resume;
	// such a row should not have come back from the in-progress query, but a card
	// that cannot act is worse than one absent.
	if item.State.PartID == "" {
		return nil
	}
	// The poster and title live on the Work, not on the episode or feature item
	// the position is keyed to (platform#9 attaches Parts to items; platform#45 stores
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
